package daemon

import (
	"fmt"
	"os"
	"sync"
)

// RotatingFile は Max バイトを超えそうになったら、Path を Path.1 に回し、古いものを Path.2、Path.3 … と送る。
// Keep 個を超えた古いファイルは消す。
type RotatingFile struct {
	Path string
	Max  int64
	Keep int

	mu   sync.Mutex
	f    *os.File
	size int64
}

func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	// 1 行がどれだけ長くても、空のファイルには必ず書く
	if r.size > 0 && r.size+int64(len(p)) > r.Max {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

func (r *RotatingFile) rotate() error {
	r.f.Close()
	r.f = nil
	os.Remove(fmt.Sprintf("%s.%d", r.Path, r.Keep))
	for i := r.Keep - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", r.Path, i), fmt.Sprintf("%s.%d", r.Path, i+1)) // なければ何もしなくてよい
	}
	if err := os.Rename(r.Path, r.Path+".1"); err != nil {
		return err
	}
	return r.open()
}
