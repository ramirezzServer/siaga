// Package cahyadsn membaca dataset batas wilayah github.com/cahyadsn/wilayah_boundaries
// (lisensi MIT). Dataset didistribusikan sebagai dump SQL gaya MySQL, jadi paket ini
// berisi parser INSERT kecil yang tidak bergantung pada database apa pun.
package cahyadsn

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrSyntax menandai dump SQL yang tidak bisa dipahami.
var ErrSyntax = errors.New("sintaks dump SQL tidak dikenali")

// Value adalah satu nilai dalam tuple INSERT.
type Value struct {
	Text string // isi literal string atau teks angka
	Null bool   // true untuk NULL
}

// Row adalah satu baris INSERT, dipetakan dari nama kolom ke nilai.
type Row map[string]Value

// splitStatements memecah dump menjadi pernyataan SQL, membuang komentar
// (--, #, /* */) dan menghormati literal string serta identifier ber-quote.
func splitStatements(src []byte) ([]string, error) {
	var (
		stmts []string
		cur   bytes.Buffer
	)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			stmts = append(stmts, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			end, err := scanQuoted(src, i)
			if err != nil {
				return nil, err
			}
			cur.Write(src[i : end+1])
			i = end
		case c == '-' && i+1 < len(src) && src[i+1] == '-', c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			cur.WriteByte('\n')
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				return nil, fmt.Errorf("%w: komentar blok tidak ditutup", ErrSyntax)
			}
			i += end + 3
			cur.WriteByte(' ')
		case c == ';':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return stmts, nil
}

// scanQuoted mengembalikan indeks tanda kutip penutup untuk literal yang dimulai di start.
// Mendukung escape backslash gaya MySQL dan kutip yang digandakan (dua kutip berurutan).
func scanQuoted(src []byte, start int) (int, error) {
	q := src[start]
	for i := start + 1; i < len(src); i++ {
		switch src[i] {
		case '\\':
			if q == '\'' {
				i++
			}
		case q:
			if i+1 < len(src) && src[i+1] == q {
				i++
				continue
			}
			return i, nil
		}
	}
	return 0, fmt.Errorf("%w: literal %c tidak ditutup (mulai byte %d)", ErrSyntax, q, start)
}

var insertHead = regexp.MustCompile("(?is)^INSERT\\s+INTO\\s+[`\"]?(\\w+)[`\"]?\\s*\\(([^)]*)\\)\\s*VALUES\\s*")

// parseInsert mengurai satu pernyataan INSERT multi-baris. Pernyataan lain diabaikan (ok=false).
func parseInsert(stmt string) (table string, rows []Row, ok bool, err error) {
	m := insertHead.FindStringSubmatchIndex(stmt)
	if m == nil {
		return "", nil, false, nil
	}
	table = stmt[m[2]:m[3]]
	var cols []string
	for c := range strings.SplitSeq(stmt[m[4]:m[5]], ",") {
		cols = append(cols, strings.Trim(strings.TrimSpace(c), "`\""))
	}

	p := &valueLexer{s: stmt, i: m[1]}
	for {
		p.skipSpace()
		if p.eof() {
			break
		}
		vals, err := p.tuple()
		if err != nil {
			return "", nil, false, err
		}
		if len(vals) != len(cols) {
			return "", nil, false, fmt.Errorf("%w: tuple berisi %d nilai, kolom %d", ErrSyntax, len(vals), len(cols))
		}
		row := make(Row, len(cols))
		for i, c := range cols {
			row[c] = vals[i]
		}
		rows = append(rows, row)
		p.skipSpace()
		if p.eof() {
			break
		}
		if !p.consume(',') {
			return "", nil, false, fmt.Errorf("%w: diharapkan ',' antar tuple di byte %d", ErrSyntax, p.i)
		}
	}
	return table, rows, true, nil
}

type valueLexer struct {
	s string
	i int
}

func (p *valueLexer) eof() bool { return p.i >= len(p.s) }

func (p *valueLexer) skipSpace() {
	for !p.eof() && strings.IndexByte(" \t\r\n", p.s[p.i]) >= 0 {
		p.i++
	}
}

func (p *valueLexer) consume(c byte) bool {
	if !p.eof() && p.s[p.i] == c {
		p.i++
		return true
	}
	return false
}

func (p *valueLexer) tuple() ([]Value, error) {
	if !p.consume('(') {
		return nil, fmt.Errorf("%w: diharapkan '(' di byte %d", ErrSyntax, p.i)
	}
	var vals []Value
	for {
		p.skipSpace()
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		vals = append(vals, v)
		p.skipSpace()
		if p.consume(')') {
			return vals, nil
		}
		if !p.consume(',') {
			return nil, fmt.Errorf("%w: diharapkan ',' atau ')' di byte %d", ErrSyntax, p.i)
		}
	}
}

func (p *valueLexer) value() (Value, error) {
	if p.eof() {
		return Value{}, fmt.Errorf("%w: nilai terpotong", ErrSyntax)
	}
	if p.s[p.i] == '\'' {
		return p.stringLiteral()
	}
	start := p.i
	for !p.eof() && strings.IndexByte(",) \t\r\n", p.s[p.i]) < 0 {
		p.i++
	}
	tok := p.s[start:p.i]
	switch {
	case tok == "":
		return Value{}, fmt.Errorf("%w: nilai kosong di byte %d", ErrSyntax, start)
	case strings.EqualFold(tok, "NULL"):
		return Value{Null: true}, nil
	default:
		return Value{Text: tok}, nil
	}
}

func (p *valueLexer) stringLiteral() (Value, error) {
	var b strings.Builder
	for p.i++; !p.eof(); p.i++ {
		c := p.s[p.i]
		switch {
		case c == '\\' && p.i+1 < len(p.s):
			p.i++
			b.WriteByte(unescape(p.s[p.i]))
		case c == '\'' && p.i+1 < len(p.s) && p.s[p.i+1] == '\'':
			p.i++
			b.WriteByte('\'')
		case c == '\'':
			p.i++
			return Value{Text: b.String()}, nil
		default:
			b.WriteByte(c)
		}
	}
	return Value{}, fmt.Errorf("%w: literal string tidak ditutup", ErrSyntax)
}

func unescape(c byte) byte {
	switch c {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'r':
		return '\r'
	case '0':
		return 0
	default:
		return c
	}
}

// ParseDump mengurai seluruh dump dan memanggil fn untuk setiap baris tabel `table`.
func ParseDump(src []byte, table string, fn func(Row) error) error {
	stmts, err := splitStatements(src)
	if err != nil {
		return err
	}
	for _, s := range stmts {
		t, rows, ok, err := parseInsert(s)
		if err != nil {
			return err
		}
		if !ok || !strings.EqualFold(t, table) {
			continue
		}
		for _, r := range rows {
			if err := fn(r); err != nil {
				return err
			}
		}
	}
	return nil
}
