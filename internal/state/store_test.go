package state

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLoadMissingFileIsEmpty(t *testing.T) {
	s, err := Load(t.TempDir())
	if err != nil || s == nil || len(s) != 0 {
		t.Fatalf("Load = %v, %v; want empty non-nil State", s, err)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new") // Save がディレクトリを作る
	want := State{"o/r#1": {Continuations: 2, Relaunches: 1, LastSentAt: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}}
	if err := want.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %v, %v; want %v", got, err, want)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestSaveKeepsOldFileWhenWriteFails(t *testing.T) {
	dir := t.TempDir()
	old := State{"o/r#1": {Relaunches: 1}}
	if err := old.Save(dir); err != nil {
		t.Fatal(err)
	}
	// 一時ファイルを作れないようにして、書き込みの途中で失敗させる
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := (State{"o/r#2": {}}).Save(dir); err == nil {
		t.Skip("directory is writable anyway (running as root?)")
	}
	got, err := Load(dir)
	if err != nil || !reflect.DeepEqual(got, old) {
		t.Fatalf("Load = %v, %v; want old %v", got, err, old)
	}
}

func TestLoadCorruptFileFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load of a corrupt file must fail")
	}
}

func TestPruneDropsIssuesNotKept(t *testing.T) {
	s := State{"o/r#1": {Continuations: 1}, "o/r#2": {Relaunches: 1}}
	s.Prune(map[string]bool{"o/r#2": true})
	if want := (State{"o/r#2": {Relaunches: 1}}); !reflect.DeepEqual(s, want) {
		t.Fatalf("Prune = %v; want %v", s, want)
	}
}
