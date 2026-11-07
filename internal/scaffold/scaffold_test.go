package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ynny-github/toban/internal/tracker"
	"github.com/ynny-github/toban/internal/workflow"
)

func TestInitWritesLoadableConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config.yaml")
	if _, err := workflow.LoadSettings(config); err != nil {
		t.Errorf("settings: %v", err)
	}
	// 例は # でコメントアウトされているので、書いただけでは Dispatch されない
	if ws, err := workflow.LoadWorkflows(config); err != nil || len(ws) != 0 {
		t.Errorf("LoadWorkflows = %v, %v, want no workflows until the example is uncommented", ws, err)
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
}

// 例の # を外すと読める Workflow になり、最初のプロンプトと続きのプロンプトが出し分けられる。
func TestExampleWorkflowRendersFirstAndContinuationPrompts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config.yaml")
	src, _ := os.ReadFile(config)
	head, example, ok := strings.Cut(string(src), "# trackers:\n")
	if !ok {
		t.Fatal("no commented example in the template")
	}
	var b strings.Builder
	b.WriteString(head + "trackers:\n")
	for _, line := range strings.Split(example, "\n") {
		b.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "#"), " ") + "\n")
	}
	if err := os.WriteFile(config, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := workflow.LoadWorkflows(config)
	if err != nil || len(ws) != 1 {
		t.Fatalf("LoadWorkflows = %v, %v, want the example", ws, err)
	}
	issue := tracker.Issue{Repo: "o/r", Number: 3, Title: "題", URL: "https://example.com/3", Body: "本文"}

	first, err1 := ws[0].Render(issue, false)
	again, err2 := ws[0].Render(issue, true)

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
