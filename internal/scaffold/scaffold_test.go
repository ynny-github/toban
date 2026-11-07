package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ynny-github/toban/internal/tracker"
	"github.com/ynny-github/toban/internal/workflow"
)

func TestInitWritesLoadableFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.LoadSettings(filepath.Join(dir, "config.yaml")); err != nil {
		t.Errorf("config.yaml: %v", err)
	}
	sample := filepath.Join(dir, "workflows", "example.md.sample")
	if _, err := workflow.Load(sample); err != nil {
		t.Errorf("sample workflow: %v", err)
	}
	if ws, _ := workflow.LoadDir(filepath.Join(dir, "workflows")); len(ws) != 0 {
		t.Errorf("the sample must not be loaded until renamed to .md, got %d workflows", len(ws))
	}
}

func TestInitDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	mine := filepath.Join(dir, "config.yaml")
	os.WriteFile(mine, []byte("interval: 1m\n"), 0o644)
	if err := Init(dir); err == nil {
		t.Fatal("Init overwrote an existing config")
	}
	if got, _ := os.ReadFile(mine); string(got) != "interval: 1m\n" {
		t.Errorf("config.yaml changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "workflows")); err == nil {
		t.Error("Init wrote other files even though it failed")
	}
}

func TestSampleRendersFirstAndContinuationPrompts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	w, err := workflow.Load(filepath.Join(dir, "workflows", "example.md.sample"))
	if err != nil {
		t.Fatal(err)
	}
	issue := tracker.Issue{Repo: "o/r", Number: 3, Title: "題", URL: "https://example.com/3", Body: "本文"}

	first, err1 := w.Render(issue, false)
	again, err2 := w.Render(issue, true)

	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if !strings.Contains(first, "WORKFLOW.md") || !strings.Contains(first, "本文") {
		t.Errorf("first prompt = %q", first)
	}
	if strings.Contains(again, "本文") || !strings.Contains(again, "https://example.com/3") {
		t.Errorf("continuation prompt = %q, want the URL and no Issue body", again)
	}
}
