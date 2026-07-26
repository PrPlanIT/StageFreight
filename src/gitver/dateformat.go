package gitver

import (
	"os"
	"strings"
	"time"
)

// friendlyLayout translates a friendly date format (e.g. YYYY-MM-DD, MM/DD/YY,
// HH:mm:ss) into a Go time layout. Tokens are translated longest-first so families
// don't collide. A Go layout ("2006-01-02", "Jan 2, 2006") contains none of these
// tokens, so it passes through unchanged — both spellings work.
//
//	YYYY→2006  YY→06  MMMM→January  MMM→Jan  MM→01  DD→02
//	HH→15 (24h)  hh→03 (12h)  mm→04  ss→05  tz→MST
func friendlyLayout(f string) string {
	repl := []struct{ from, to string }{
		{"YYYY", "2006"}, {"YY", "06"},
		{"MMMM", "January"}, {"MMM", "Jan"}, {"MM", "01"},
		{"DD", "02"},
		{"HH", "15"}, {"hh", "03"},
		{"mm", "04"}, {"ss", "05"},
		{"tz", "MST"},
	}
	for _, r := range repl {
		f = strings.ReplaceAll(f, r.from, r.to)
	}
	return f
}

// renderLocation is the timezone applied to rendered dates: $TZ if set and valid,
// otherwise UTC (the reproducible default).
func renderLocation() *time.Location {
	if tz := strings.TrimSpace(os.Getenv("TZ")); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return time.UTC
}

// formatFriendly renders t using a friendly (or Go) layout, in the render timezone.
func formatFriendly(t time.Time, format string) string {
	return t.In(renderLocation()).Format(friendlyLayout(format))
}
