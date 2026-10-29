package merge

import "testing"

func TestText(t *testing.T) {
	base := "a\nb\nc\nd\ne\nf\ng\n"
	cases := []struct {
		name, ours, theirs, want string
		ok                       bool
	}{
		{"one side", "a\nB\nc\nd\ne\nf\ng\n", base, "a\nB\nc\nd\ne\nf\ng\n", true},
		{"far apart", "a\nB\nc\nd\ne\nf\ng\n", "a\nb\nc\nd\ne\nF\ng\n", "a\nB\nc\nd\ne\nF\ng\n", true},
		{"same change both sides", "a\nX\nc\nd\ne\nf\ng\n", "a\nX\nc\nd\ne\nf\ng\n", "a\nX\nc\nd\ne\nf\ng\n", true},
		{"different change same line", "a\nX\nc\nd\ne\nf\ng\n", "a\nY\nc\nd\ne\nf\ng\n", "", false},
		{"adjacent lines conflict", "a\nB\nc\nd\ne\nf\ng\n", "a\nb\nC\nd\ne\nf\ng\n", "", false},
		{"insert and far edit", "a\nb\nNEW\nc\nd\ne\nf\ng\n", "a\nb\nc\nd\ne\nf\nG\n", "a\nb\nNEW\nc\nd\ne\nf\nG\n", true},
		{"both insert at one point", "a\nours\nb\nc\nd\ne\nf\ng\n", "a\ntheirs\nb\nc\nd\ne\nf\ng\n", "", false},
		{"delete vs far edit", "a\nc\nd\ne\nf\ng\n", "a\nb\nc\nd\ne\nF\ng\n", "a\nc\nd\ne\nF\ng\n", true},
		{"delete vs edit same line", "a\nc\nd\ne\nf\ng\n", "a\nB\nc\nd\ne\nf\ng\n", "", false},
		{"append both ends", "top\na\nb\nc\nd\ne\nf\ng\n", "a\nb\nc\nd\ne\nf\ng\nbottom\n", "top\na\nb\nc\nd\ne\nf\ng\nbottom\n", true},
		{"trailing newline change merges with a far edit", "a\nb\nc\nd\ne\nf\ng", "A\nb\nc\nd\ne\nf\ng\n", "A\nb\nc\nd\ne\nf\ng", true},
		{"crlf preserved", "a\r\nb\r\nc\r\n", "a\r\nb\r\nc\r\n", "a\r\nb\r\nc\r\n", true},
	}
	for _, c := range cases {
		got, ok := Text(base, c.ours, c.theirs)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: got %q ok=%v, want %q ok=%v", c.name, got, ok, c.want, c.ok)
		}
	}
	// A real fork shape: our patch near the top, upstream's release bumps a version below.
	fbase := "FROM alpine:3.21\nRUN apk add nut\nCOPY entrypoint.sh /\nENV NUT_VERSION=2.8.2\nCMD [\"upsd\"]\n"
	fours := "FROM alpine:3.21\nRUN apk add nut tini\nCOPY entrypoint.sh /\nENV NUT_VERSION=2.8.2\nCMD [\"upsd\"]\n"
	ftheirs := "FROM alpine:3.21\nRUN apk add nut\nCOPY entrypoint.sh /\nENV NUT_VERSION=2.8.3\nCMD [\"upsd\"]\n"
	if got, ok := Text(fbase, fours, ftheirs); !ok || got != "FROM alpine:3.21\nRUN apk add nut tini\nCOPY entrypoint.sh /\nENV NUT_VERSION=2.8.3\nCMD [\"upsd\"]\n" {
		t.Errorf("fork shape: %q ok=%v", got, ok)
	}
}
