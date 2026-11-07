// Package daemon は toban run を背景で動かし続けるための、ロック、PID、起動、停止、ログの回転を持つ。
package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ynny-github/toban/internal/tick"
)

// Paths は state ディレクトリの中で、run が使うファイルの場所。
type Paths struct{ Lock, PID, Log string }

func PathsIn(dir string) Paths {
	return Paths{Lock: filepath.Join(dir, "run.lock"), PID: filepath.Join(dir, "run.pid"), Log: filepath.Join(dir, "toban.log")}
}

// Loop は tick を繰り返す。tick が終わってから interval を待つので、周は重ならない。
// ctx が終わったら、実行中の tick は最後まで待ってから戻る（途中で止めると Dispatch が中途半端になる）。
func Loop(ctx context.Context, interval func() time.Duration, tick func()) {
	for ctx.Err() == nil {
		tick()
		select {
		case <-ctx.Done():
		case <-time.After(interval()):
		}
	}
}

// Status は run が動いているかを返す。判断はロックで行い、PID ファイルは信用しない（落ちても古い PID が残るため）。
// 動いているのに PID ファイルが読めないときは、pid は 0 になる。
func Status(p Paths) (pid int, running bool, err error) {
	release, ok, err := tick.Lock(p.Lock)
	if err != nil {
		return 0, false, err
	}
	if ok {
		release()
		return 0, false, nil
	}
	b, _ := os.ReadFile(p.PID)
	pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	return pid, true, nil
}

// Acquire は run の間ずっと持つロックを取り、PID ファイルを書く。すでに動いていればエラーにする。
func Acquire(p Paths) (release func(), err error) {
	unlock, ok, err := tick.Lock(p.Lock)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("toban run is already running")
	}
	if err := os.WriteFile(p.PID, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		unlock()
		return nil, err
	}
	return func() { os.Remove(p.PID); unlock() }, nil
}

// Start は self を args で、新しいセッションに切り離して起動する。ログは p.Log に追記する。
// run がロックを取るまで（最大 5 秒）待ち、取れなければエラーにする。
func Start(self string, args []string, p Paths) (int, error) {
	if pid, running, err := Status(p); err != nil || running {
		if err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("already running (pid %d)", pid)
	}
	logFile, err := os.OpenFile(p.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer logFile.Close()
	cmd := exec.Command(self, args...)
	cmd.Env = append(os.Environ(), "TOBAN_LOG="+p.Log)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		select {
		case err := <-exited:
			return 0, fmt.Errorf("toban run exited right away (%v); see %s", err, p.Log)
		case <-time.After(100 * time.Millisecond):
		}
		if pid, running, _ := Status(p); running && pid == cmd.Process.Pid {
			return pid, nil
		}
	}
	return 0, fmt.Errorf("toban run did not start within 5s; see %s", p.Log)
}

// Stop は run に SIGTERM を送り、終わるまで最大 wait 待つ。
// run は実行中の tick を終えてから止まるので、間に合わなければ stopped=false を返す。
func Stop(p Paths, wait time.Duration) (stopped bool, err error) {
	pid, running, err := Status(p)
	if err != nil {
		return false, err
	}
	if !running {
		return true, nil
	}
	if pid == 0 {
		return false, fmt.Errorf("toban run is running but %s is unreadable", p.PID)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return false, err
	}
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if _, running, _ := Status(p); !running {
			return true, nil
		}
	}
	return false, nil
}
