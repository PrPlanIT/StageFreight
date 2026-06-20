package lint

import (
	"bytes"
	"testing"
)

func TestClassifyContent(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want ContentKind
	}{
		{"empty", nil, ContentText},
		{"ascii", []byte("hello world\n"), ContentText},
		{"utf8-emoji", []byte("ship it \xF0\x9F\x9A\x80\n"), ContentText},
		{"png-magic", append([]byte("\x89PNG\r\n\x1a\n"), 0x00, 0x01, 0x02), ContentBinary},
		{"null-byte", []byte("abc\x00def text"), ContentBinary},
		// UTF-16 has NUL bytes but the BOM must win over the NUL scan → Ambiguous.
		{"utf16-bom", []byte{0xFF, 0xFE, 'h', 0x00, 'i', 0x00}, ContentAmbiguous},
		{"control-heavy", bytes.Repeat([]byte{0x01, 0x02, 0x03, 0x04}, 50), ContentBinary},
		// mostly-text with trailing invalid bytes → partial validity → Ambiguous.
		{"partial-validity", append([]byte("mostly clean text content here "), 0xFF, 0xFE, 0xFF, 0xFE), ContentAmbiguous},
	}
	for _, c := range cases {
		if got := classifyContent(c.data).Kind; got != c.want {
			t.Errorf("%s: kind=%d want=%d", c.name, got, c.want)
		}
	}
}

func TestContentIsTextFailsOpen(t *testing.T) {
	if !(Content{}).IsText() { // zero value must behave as text (no silent suppression)
		t.Error("zero-value Content must be text")
	}
	if (Content{Kind: ContentAmbiguous}).IsText() {
		t.Error("ambiguous must NOT be treated as text")
	}
}
