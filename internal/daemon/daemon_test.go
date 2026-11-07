package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoopWaitsForTheRunningTickBeforeReturning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks, finished := 0, false
	Loop(ctx, func() time.Duration { return time.Millisecond }, func() {
		ticks++
		if ticks == 3 {
			cancel() // tick の途中で止められても、この tick は最後まで走る
			time.Sleep(20 * time.Millisecond)
			finished = true
		}
	})
	if ticks != 3 || !finished {
		t.Errorf("ticks = %d, finished = %v, want 3 and true", ticks, finished)
	}
}

func TestAcquireExcludesASecondRunAndStatusFollowsTheLock(t *testing.T) {
	p := PathsIn(t.TempDir())
	if _, running, _ := Status(p); running {
		t.Fatal("running before Acquire")
	}

	release, err := Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(p); err == nil {
		t.Error("a second Acquire succeeded")
	}
	pid, running, _ := Status(p)
	if !running || pid != os.Getpid() {
		t.Errorf("Status = %d, %v, want this process running", pid, running)
	}

	release()
	if _, running, _ := Status(p); running {
		t.Error("still running after release")
	}
	if _, err := os.Stat(p.PID); err == nil {
		t.Error("pid file was left behind")
	}
}

func TestRotatingFileShiftsOldFilesAndDropsBeyondKeep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "toban.log")
	r := &RotatingFile{Path: path, Max: 10, Keep: 2}

	for _, line := range []string{"aaaaaa\n", "bbbbbb\n", "cccccc\n", "dddddd\n"} { // 7 バイトずつ。2 行目から回る
		if _, err := r.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}

	for name, want := range map[string]string{path: "dddddd\n", path + ".1": "cccccc\n", path + ".2": "bbbbbb\n"} {
		if got, _ := os.ReadFile(name); string(got) != want {
			t.Errorf("%s = %q, want %q", filepath.Base(name), got, want)
		}
	}
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Error("kept more files than Keep")
	}
}

func TestRotatingFileWritesALongLineToAnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "toban.log")
	r := &RotatingFile{Path: path, Max: 4, Keep: 1}
	if _, err := r.Write([]byte(strings.Repeat("x", 9))); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); len(got) != 9 {
		t.Errorf("wrote %d bytes, want 9", len(got))
	}
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Error("rotated an empty file")
	}
}
