package schedule

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestPlistRunsTickAtTheInterval(t *testing.T) {
	got := Plist(Job{
		Toban:     "/Users/dev/go/bin/toban",
		ConfigDir: "/Users/dev/.config/toban",
		Tools:     []string{"/opt/homebrew/bin/gh", "/Users/dev/.local/bin/orca", "/opt/homebrew/bin/git"},
		Interval:  2 * time.Minute,
		Log:       "/Users/dev/Library/Logs/toban/tick.log",
	})

	want, err := os.ReadFile("testdata/dev.toban.plist")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("plist:\n%s\nwant:\n%s", got, want)
	}
}

// launchd の plist は XML なので、パスに & や < があっても壊れてはいけない。
func TestPlistEscapesPaths(t *testing.T) {
	got := string(Plist(Job{Toban: "/a&b/<toban>", ConfigDir: "/c", Interval: time.Minute, Log: "/l"}))
	if !strings.Contains(got, "<string>/a&amp;b/&lt;toban&gt;</string>") {
		t.Errorf("path is not escaped:\n%s", got)
	}
}
