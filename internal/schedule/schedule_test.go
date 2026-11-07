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

func TestSystemdUnitsRunTickAtTheInterval(t *testing.T) {
	svc, tmr := SystemdUnits(Job{
		Toban:     "/home/dev/.local/bin/toban",
		ConfigDir: "/home/dev/toban-config",
		Tools:     []string{"/usr/local/bin/gh", "/home/dev/.local/bin/orca", "/usr/bin/git"},
		Interval:  2 * time.Minute,
	})

	for name, got := range map[string][]byte{"toban.service": svc, "toban.timer": tmr} {
		want, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("%s:\n%s\nwant:\n%s", name, got, want)
		}
	}
}

// systemd は " と \ と % を特別扱いするので、パスに含まれていても 1 語のまま渡せなければならない。
func TestSystemdUnitsQuotePaths(t *testing.T) {
	svc, _ := SystemdUnits(Job{Toban: `/a b/"t"\x%h`, ConfigDir: "/c", Interval: time.Minute})
	if !strings.Contains(string(svc), `ExecStart="/a b/\"t\"\\x%%h" "-config" "/c" "tick"`) {
		t.Errorf("path is not quoted:\n%s", svc)
	}
}
