package lint

import (
	"bufio"
	"io"
)

// MaxLineBytes is the largest single line the line-oriented lint modules accept.
// bufio.Scanner's default token cap is 64 KiB, which aborts a scan with
// "bufio.Scanner: token too long" on files that put a very long line on disk — a
// minified JSON (e.g. a Grafana dashboard emitted on one line) routinely exceeds it.
// 16 MiB covers any realistic single-line file without a real memory cost (the buffer
// starts at 64 KiB and only grows toward the cap when a line actually needs it).
const MaxLineBytes = 16 << 20

// NewLineScanner returns a bufio.Scanner that tolerates very long lines, so one
// oversized line never aborts a line-oriented lint module mid-file. Use this instead
// of bufio.NewScanner anywhere a module scans arbitrary repository files.
func NewLineScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), MaxLineBytes)
	return s
}
