package catalog

import (
	"bufio"
	"bytes"
	"io"
)

// sniffer mengintip baris pertama untuk menentukan pemisah kolom tanpa
// membaca ulang file.
type sniffer struct {
	*bufio.Reader
}

func newSniffer(r io.Reader) sniffer { return sniffer{bufio.NewReaderSize(r, 64<<10)} }

func (s sniffer) hasTab() bool {
	b, _ := s.Peek(4096)
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		b = b[:i]
	}
	return bytes.IndexByte(b, '\t') >= 0
}
