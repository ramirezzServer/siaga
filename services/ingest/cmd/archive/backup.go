package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ramirezzServer/siaga/libs/go/platform/envx"
	"github.com/ramirezzServer/siaga/services/ingest/internal/adapters/archiveurl"
	"github.com/ramirezzServer/siaga/services/ingest/internal/app/archivebackup"
	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/retention"
)

// now dapat diganti test.
var now = time.Now

// errVerify dikembalikan backup-verify dan restore bila ada bagian rusak.
var errVerify = errors.New("ada bagian cadangan rusak")

// placeholder adalah nilai contoh di .env.example yang belum diganti.
const placeholder = "ganti-saya"

// backupEnv adalah konfigurasi cadangan dari environment (ADR 0022).
type backupEnv struct {
	url   string
	creds archiveurl.Credentials
	// retention kosong = retention.Default().
	retention string
}

func readBackupEnv(env *envx.Reader) backupEnv {
	return backupEnv{
		url: strings.TrimSpace(env.Default("ARCHIVE_BACKUP_URL", "")),
		creds: archiveurl.Credentials{
			AccessKeyID:     strings.TrimSpace(env.Default("ARCHIVE_BACKUP_S3_ACCESS_KEY_ID", "")),
			SecretAccessKey: strings.TrimSpace(env.Default("ARCHIVE_BACKUP_S3_SECRET_ACCESS_KEY", "")),
		},
		retention: strings.TrimSpace(env.Default("ARCHIVE_RETENTION", "")),
	}
}

// openBackup membuka penyimpanan cadangan dan memastikan bisa dipakai.
func openBackup(ctx context.Context, raw string, creds archiveurl.Credentials, other string) (archiveurl.Store, error) {
	if raw == "" {
		return nil, errors.New("URL cadangan kosong: isi -backup atau ARCHIVE_BACKUP_URL (lihat docs/setup/backblaze-b2.md)")
	}
	if strings.Contains(raw, "://") && !strings.HasPrefix(raw, "file://") &&
		(strings.Contains(creds.AccessKeyID, placeholder) || strings.Contains(creds.SecretAccessKey, placeholder) ||
			creds.AccessKeyID == "" || creds.SecretAccessKey == "") {
		return nil, errors.New("kredensial cadangan belum diisi: ARCHIVE_BACKUP_S3_ACCESS_KEY_ID dan ARCHIVE_BACKUP_S3_SECRET_ACCESS_KEY di .env")
	}
	store, err := archiveurl.Open(raw, creds, nil)
	if err != nil {
		return nil, fmt.Errorf("cadangan: %w", err)
	}
	if other != "" && store.Location() == other {
		return nil, fmt.Errorf("cadangan dan arsip sama (%s)", other)
	}
	if err := store.Check(ctx); err != nil {
		return nil, fmt.Errorf("cadangan: %w", err)
	}
	return store, nil
}

// dayFlags mendaftarkan -from, -to, dan -connectors.
type dayFlags struct {
	from, to, connectors *string
}

func addDayFlags(fs *flag.FlagSet, fromHelp string) dayFlags {
	return dayFlags{
		from:       fs.String("from", "", fromHelp),
		to:         fs.String("to", "", "hari UTC terakhir (YYYY-MM-DD, inklusif)"),
		connectors: fs.String("connectors", "", "daftar konektor dipisah koma (kosong = semua)"),
	}
}

func (d dayFlags) filter() (archivebackup.Filter, error) {
	var f archivebackup.Filter
	var err error
	if f.From, err = parseDay(*d.from, "-from"); err != nil {
		return f, err
	}
	if f.To, err = parseDay(*d.to, "-to"); err != nil {
		return f, err
	}
	if !f.From.IsZero() && !f.To.IsZero() && f.To.Before(f.From) {
		return f, errors.New("-to sebelum -from")
	}
	for c := range strings.SplitSeq(*d.connectors, ",") {
		if c = strings.TrimSpace(c); c != "" {
			f.Connectors = append(f.Connectors, c)
		}
	}
	return f, nil
}

func parseDay(s, flagName string) (time.Time, error) {
	if s = strings.TrimSpace(s); s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s harus YYYY-MM-DD: %q", flagName, s)
	}
	return t, nil
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// runBackupCmd menjalankan subperintah cadangan dan retensi; handled false
// bila cmd bukan salah satunya.
func runBackupCmd(ctx context.Context, cmd string, rest []string, env *envx.Reader, defArchive string, creds archiveurl.Credentials, stdout, stderr io.Writer) (handled bool, err error) {
	be := readBackupEnv(env)
	fs := flag.NewFlagSet("archive "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	backupURL := fs.String("backup", be.url, "URL cadangan (bawaan ARCHIVE_BACKUP_URL)")
	asJSON := fs.Bool("json", false, "cetak laporan JSON")
	switch cmd {
	case "backup":
		archive := fs.String("archive", defArchive, "URL arsip panas")
		days := addDayFlags(fs, "hari UTC pertama (YYYY-MM-DD)")
		if err := parse(fs, rest); err != nil {
			return true, err
		}
		f, err := days.filter()
		if err != nil {
			return true, err
		}
		src, err := open(*archive, creds)
		if err != nil {
			return true, err
		}
		dst, err := openBackup(ctx, *backupURL, be.creds, src.Location())
		if err != nil {
			return true, err
		}
		start := time.Now()
		rep, err := archivebackup.Backup{
			Src: src, Dst: dst, Now: now,
			Progress: func(p archivebackup.PartReport) {
				fmt.Fprintf(stderr, "… %s %s bagian %d: %d objek\n", p.Connector, p.Day, p.Part, p.Objects)
			},
		}.Run(ctx, f)
		if *asJSON {
			_ = printJSON(stdout, rep)
		} else {
			writeBackup(stdout, src.Location(), dst.Location(), rep, time.Since(start))
		}
		return true, err
	case "backup-verify":
		days := addDayFlags(fs, "hari UTC pertama (bawaan: dua hari sebelum hari ini)")
		all := fs.Bool("all", false, "periksa semua hari di cadangan")
		if err := parse(fs, rest); err != nil {
			return true, err
		}
		f, err := days.filter()
		if err != nil {
			return true, err
		}
		if !*all && f.From.IsZero() && f.To.IsZero() {
			today := retention.Day(now())
			f.From, f.To = today.AddDate(0, 0, -2), today.AddDate(0, 0, -1)
		}
		dst, err := openBackup(ctx, *backupURL, be.creds, "")
		if err != nil {
			return true, err
		}
		rep, err := archivebackup.Verify(ctx, dst, f)
		if *asJSON {
			_ = printJSON(stdout, rep)
		} else {
			fmt.Fprintf(stdout, "%s: %d bagian, %d objek utuh, %d bagian rusak%s\n", dst.Location(), rep.Parts, rep.Objects, rep.Corrupt, rangeNote(f))
			for _, s := range rep.Samples {
				fmt.Fprintln(stdout, "  "+s)
			}
		}
		if err == nil && rep.Corrupt > 0 {
			err = errVerify
		}
		return true, err
	case "restore":
		into := fs.String("into", defArchive, "URL arsip tujuan (bawaan INGEST_ARCHIVE_URL; folder juga boleh)")
		days := addDayFlags(fs, "hari UTC pertama (YYYY-MM-DD, wajib)")
		if err := parse(fs, rest); err != nil {
			return true, err
		}
		f, err := days.filter()
		if err != nil {
			return true, err
		}
		if f.From.IsZero() {
			return true, errors.New("-from wajib diisi (pulihkan per rentang hari)")
		}
		to, err := open(*into, creds)
		if err != nil {
			return true, fmt.Errorf("tujuan: %w", err)
		}
		if err := to.Check(ctx); err != nil {
			return true, err
		}
		dst, err := openBackup(ctx, *backupURL, be.creds, to.Location())
		if err != nil {
			return true, err
		}
		rep, err := archivebackup.Restore(ctx, dst, to, f)
		if *asJSON {
			_ = printJSON(stdout, rep)
		} else {
			fmt.Fprintf(stdout, "%s → %s: %d bagian, %d dipulihkan, %d sudah ada, %d bagian rusak%s\n",
				dst.Location(), to.Location(), rep.Parts, rep.Restored, rep.Existing, rep.Corrupt, rangeNote(f))
			for _, s := range rep.Samples {
				fmt.Fprintln(stdout, "  "+s)
			}
		}
		if err == nil && rep.Corrupt > 0 {
			err = errVerify
		}
		return true, err
	case "prune":
		archive := fs.String("archive", defArchive, "URL arsip panas")
		days := addDayFlags(fs, "hari UTC pertama")
		policySpec := fs.String("retention", be.retention, "aturan konektor=hari,… (bawaan ARCHIVE_RETENTION, kosong = aturan ADR 0022)")
		apply := fs.Bool("apply", false, "benar-benar menghapus (tanpa ini hanya menghitung)")
		maxDelete := fs.Int("max-delete", 200000, "batas objek yang dihapus per jalan (0 = tanpa batas)")
		if err := parse(fs, rest); err != nil {
			return true, err
		}
		f, err := days.filter()
		if err != nil {
			return true, err
		}
		policy := retention.Default()
		if *policySpec != "" {
			if policy, err = retention.Parse(*policySpec); err != nil {
				return true, err
			}
		}
		src, err := open(*archive, creds)
		if err != nil {
			return true, err
		}
		dst, err := openBackup(ctx, *backupURL, be.creds, src.Location())
		if err != nil {
			return true, err
		}
		rep, err := archivebackup.Prune{
			Src: src, Backup: dst, Policy: policy, Now: now, Apply: *apply, MaxDelete: *maxDelete,
		}.Run(ctx, f)
		if *asJSON {
			_ = printJSON(stdout, rep)
		} else {
			writePrune(stdout, src.Location(), policy, rep)
		}
		return true, err
	default:
		return false, nil
	}
}

func rangeNote(f archivebackup.Filter) string {
	if f.From.IsZero() && f.To.IsZero() {
		return ""
	}
	day := func(t time.Time) string {
		if t.IsZero() {
			return "…"
		}
		return t.Format(time.DateOnly)
	}
	return fmt.Sprintf(" (hari %s s.d. %s)", day(f.From), day(f.To))
}

func writeBackup(w io.Writer, src, dst string, rep archivebackup.BackupReport, elapsed time.Duration) {
	fmt.Fprintf(w, "%s → %s (hari sebelum %s UTC)\n", src, dst, rep.ClosedBefore)
	if len(rep.Parts) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "konektor\thari\tbagian\tobjek\tmentah\tbundel")
		var payload, bundle int64
		for _, p := range rep.Parts {
			payload += p.PayloadBytes
			bundle += p.BundleBytes
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\n", p.Connector, p.Day, p.Part, p.Objects, human(p.PayloadBytes), human(p.BundleBytes))
		}
		fmt.Fprintf(tw, "total\t\t%d\t\t%s\t%s\n", len(rep.Parts), human(payload), human(bundle))
		_ = tw.Flush()
	}
	fmt.Fprintf(w, "%d bagian baru, %d konektor-hari sudah lengkap, %d objek rusak dilewati, %s\n",
		len(rep.Parts), rep.Covered, rep.Corrupt, elapsed.Round(time.Millisecond))
	if rep.Foreign > 0 {
		fmt.Fprintf(w, "%d objek dengan kunci tidak baku tidak dicadangkan\n", rep.Foreign)
	}
	for _, s := range rep.Samples {
		fmt.Fprintln(w, "  "+s)
	}
}

func writePrune(w io.Writer, src string, policy retention.Policy, rep archivebackup.PruneReport) {
	verb := "akan dihapus"
	if rep.Applied {
		verb = "dihapus"
	}
	fmt.Fprintf(w, "%s, aturan %s\n", src, policy)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "konektor\tsimpan\tsebelum (UTC)\thari\t%s\tukuran\tbelum dicadangkan\n", verb)
	var n int
	var size int64
	for _, c := range rep.Connectors {
		n += c.Deleted
		size += c.Bytes
		fmt.Fprintf(tw, "%s\t%d hari\t%s\t%d\t%d\t%s\t%d\n", c.Connector, c.KeepDays, c.Cutoff, c.Days, c.Deleted, human(c.Bytes), c.NotBackedUp)
	}
	fmt.Fprintf(tw, "total\t\t\t\t%d\t%s\t\n", n, human(size))
	_ = tw.Flush()
	if rep.Limited {
		fmt.Fprintln(w, "batas -max-delete tercapai; jalankan lagi untuk sisanya")
	}
	if !rep.Applied {
		fmt.Fprintln(w, "tidak ada yang dihapus; tambahkan -apply untuk menghapus")
	}
	for _, s := range rep.Samples {
		fmt.Fprintln(w, "  "+s)
	}
}
