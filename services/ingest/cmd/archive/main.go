// Command archive merawat arsip payload mentah ingest (ADR 0014).
//
//	archive ls     [-archive URL] [-prefix P] [-keys]   # ringkasan per konektor
//	archive verify [-archive URL] [-prefix P]           # gzip utuh dan SHA-256 cocok
//	archive cp     -from URL [-to URL] [-prefix P]      # salin yang belum ada di tujuan
//
// -archive dan -to bawaan INGEST_ARCHIVE_URL; kredensial S3 dari
// ARCHIVE_S3_ACCESS_KEY_ID dan ARCHIVE_S3_SECRET_ACCESS_KEY (dipakai untuk
// sumber maupun tujuan S3).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ramirezzServer/siaga/libs/go/platform/envx"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/archiveurl"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/archivetool"
)

var wib = time.FixedZone("WIB", 7*3600)

// errCorrupt dikembalikan verify bila ada objek rusak.
var errCorrupt = errors.New("ada objek arsip rusak")

const usage = `pemakaian: archive <ls|verify|cp> [opsi]
  ls      ringkasan arsip per konektor (-keys untuk daftar kunci)
  verify  periksa setiap objek: kunci baku, gzip utuh, SHA-256 cocok
  cp      salin objek yang belum ada dari -from ke -to (bawaan INGEST_ARCHIVE_URL)`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], envx.OS, os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "archive:", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, lookup envx.Lookup, stdout, stderr io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, usage)
		return errors.New("subperintah kosong")
	}
	env := envx.NewReader(lookup)
	defArchive := env.Default("INGEST_ARCHIVE_URL", env.Default("INGEST_ARCHIVE_DIR", ""))
	creds := archiveurl.Credentials{
		AccessKeyID:     strings.TrimSpace(env.Default("ARCHIVE_S3_ACCESS_KEY_ID", "")),
		SecretAccessKey: strings.TrimSpace(env.Default("ARCHIVE_S3_SECRET_ACCESS_KEY", "")),
	}
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet("archive "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	prefix := fs.String("prefix", "", "hanya kunci berawalan ini, misal bmkg-autogempa/2026/09/")
	switch cmd {
	case "ls":
		archive := fs.String("archive", defArchive, "URL arsip")
		keys := fs.Bool("keys", false, "cetak setiap kunci beserta ukurannya")
		if err := parse(fs, rest); err != nil {
			return err
		}
		store, err := open(*archive, creds)
		if err != nil {
			return err
		}
		if *keys {
			for o, err := range store.List(ctx, *prefix, "") {
				if err != nil {
					return err
				}
				fmt.Fprintf(stdout, "%s\t%d\n", o.Key, o.Size)
			}
			return nil
		}
		sum, err := archivetool.Summarize(ctx, store, *prefix)
		if err != nil {
			return err
		}
		return writeSummary(stdout, store.Location(), sum)
	case "verify":
		archive := fs.String("archive", defArchive, "URL arsip")
		if err := parse(fs, rest); err != nil {
			return err
		}
		store, err := open(*archive, creds)
		if err != nil {
			return err
		}
		rep, err := archivetool.Verify(ctx, store, *prefix)
		fmt.Fprintf(stdout, "%s: %d objek, %d utuh, %d rusak\n", store.Location(), rep.Objects, rep.OK, rep.Corrupt)
		for _, s := range rep.Samples {
			fmt.Fprintln(stdout, "  "+s)
		}
		if err != nil {
			return err
		}
		if rep.Corrupt > 0 {
			return errCorrupt
		}
		return nil
	case "cp":
		from := fs.String("from", "", "URL arsip sumber (wajib)")
		to := fs.String("to", defArchive, "URL arsip tujuan")
		if err := parse(fs, rest); err != nil {
			return err
		}
		if *from == "" {
			return errors.New("-from wajib diisi")
		}
		src, err := open(*from, creds)
		if err != nil {
			return fmt.Errorf("sumber: %w", err)
		}
		dst, err := open(*to, creds)
		if err != nil {
			return fmt.Errorf("tujuan: %w", err)
		}
		if src.Location() == dst.Location() {
			return fmt.Errorf("sumber dan tujuan sama (%s)", src.Location())
		}
		if err := dst.Check(ctx); err != nil {
			return err
		}
		start := time.Now()
		rep, err := archivetool.Copy(ctx, src, dst, *prefix, func(r archivetool.CopyReport) {
			if r.Copied%500 == 0 {
				fmt.Fprintf(stderr, "… %d objek tersalin\n", r.Copied)
			}
		})
		fmt.Fprintf(stdout, "%s → %s: %d disalin (%d byte), %d sudah ada, %d rusak dilewati, %s\n",
			src.Location(), dst.Location(), rep.Copied, rep.Bytes, rep.Existing, rep.Skipped, time.Since(start).Round(time.Millisecond))
		for _, s := range rep.Samples {
			fmt.Fprintln(stdout, "  "+s)
		}
		return err
	default:
		fmt.Fprintln(stderr, usage)
		return fmt.Errorf("subperintah %q tidak dikenal", cmd)
	}
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("argumen tidak dikenal: %v", fs.Args())
	}
	return nil
}

func open(raw string, creds archiveurl.Credentials) (archiveurl.Store, error) {
	if raw == "" {
		return nil, errors.New("arsip kosong: isi -archive/-to atau INGEST_ARCHIVE_URL")
	}
	return archiveurl.Open(raw, creds, nil)
}

func writeSummary(w io.Writer, location string, sum archivetool.Summary) error {
	fmt.Fprintln(w, location)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "konektor\tobjek\tukuran\tpertama (WIB)\tterakhir (WIB)")
	var objects int
	var size int64
	for _, c := range sum.Connectors {
		objects += c.Objects
		size += c.Bytes
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", c.Connector, c.Objects, human(c.Bytes),
			c.First.In(wib).Format("2006-01-02 15:04"), c.Last.In(wib).Format("2006-01-02 15:04"))
	}
	fmt.Fprintf(tw, "total\t%d\t%s\t\t\n", objects, human(size))
	if err := tw.Flush(); err != nil {
		return err
	}
	if sum.Foreign > 0 {
		fmt.Fprintf(w, "%d objek dengan kunci tidak baku (bukan hasil ingest)\n", sum.Foreign)
	}
	return nil
}

func human(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
