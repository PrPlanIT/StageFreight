package config

import "testing"

func TestMessageHasTrailer(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"trailer last line", "docs: refresh\n\nbody\n\n" + GeneratedByTrailer, true},
		{"trailer only", GeneratedByTrailer, true},
		{"mid-line prose mention", "fix(ci): bypass skip\n\nThe rule skips (" + GeneratedByTrailer + ") commits.", false},
		{"subject mention", "note about " + GeneratedByTrailer + " handling", false},
		{"absent", "feat: add thing\n\nno trailer here", false},
		{"CRLF trailer", "docs: refresh\r\n\r\n" + GeneratedByTrailer + "\r", true},
	}
	for _, c := range cases {
		if got := MessageHasTrailer(c.msg, GeneratedByTrailer); got != c.want {
			t.Errorf("%s: MessageHasTrailer = %v, want %v", c.name, got, c.want)
		}
	}
}
