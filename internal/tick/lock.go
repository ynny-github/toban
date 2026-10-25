package tick

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// Lock は path のファイルに排他ロックをかける。ほかの tick が持っていれば ok=false を返す。
// ロックはプロセスが終われば OS が外すので、落ちても残らない。
func Lock(path string) (release func(), ok bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() { f.Close() }, true, nil
}
