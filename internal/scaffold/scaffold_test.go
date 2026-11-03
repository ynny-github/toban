package scaffold

import (
	"os"
	"path/filepath"
	"testing"

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
