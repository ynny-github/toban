package state

import (
	"path/filepath"
	"testing"
)

func TestDirPrefersOverrideThenXDGThenHome(t *testing.T) {
	t.Setenv("HOME", "/home/dev")
	t.Setenv("XDG_STATE_HOME", "")
	if got, _ := Dir(""); got != "/home/dev/.local/state/toban" {
		t.Errorf("home default = %q", got)
	}
	t.Setenv("XDG_STATE_HOME", "/x")
	if got, _ := Dir(""); got != "/x/toban" {
		t.Errorf("XDG = %q", got)
	}
	if got, _ := Dir("/mine"); got != "/mine" {
		t.Errorf("override = %q", got)
	}
	if got, _ := Dir("rel"); !filepath.IsAbs(got) {
		t.Errorf("override must be made absolute, got %q", got)
	}
}
