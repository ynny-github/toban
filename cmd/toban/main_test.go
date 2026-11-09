package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 使用量の上限を超えていれば、Working State の Issue の継続や起動し直しも含めて、orca に何も頼まない。
func TestRunTickStopsAtTheUsageGate(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	// 5 時間枠を 95% 使った、新しい利用率を返す偽の orca。呼ばれた引数を記録する
	script := `#!/bin/sh
echo "$@" >> ` + calls + `
cat <<EOF
{"ok": true, "result": {"rateLimits": {"claude": {"session": {"usedPercent": 95}, "weekly": {"usedPercent": 1}, "updatedAt": $(date +%s)000, "error": null}}}}
EOF
`
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "orca"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	stateDir := filepath.Join(dir, "state")

	if err := runTick(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), filepath.Join(dir, "config"), stateDir); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "account list --json" {
		t.Errorf("orca calls = %q, want only the usage check", got)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state.json")); !os.IsNotExist(err) {
		t.Errorf("state.json was touched: %v", err)
	}
}
