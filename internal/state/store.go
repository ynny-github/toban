package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// fileName は state ディレクトリの中で、tick をまたぐヒントを置くファイル。
// State の真実は Project にあり、このファイルが消えても余分に送るだけで済む（ADR 0001）。
const fileName = "state.json"

// State は Issue ごとのヒントで、キーは Issue の ID（例: "owner/repo#1"）。
// tick のロックを取っている間だけ Load と Save を使う。
type State map[string]Entry

// Entry は 1 つの Issue について覚えておくこと。
type Entry struct {
	Continuations int       `json:"continuations"`
	Relaunches    int       `json:"relaunches"`
	LastSentAt    time.Time `json:"last_sent_at"`
}

// Load は dir の state.json を読む。ファイルがなければ空の State を返す。
// 壊れていて読めないときは、回数の上限が効かなくならないようエラーにする。
func Load(dir string) (State, error) {
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return nil, err
	}
	s := State{}
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s is corrupt; fix or delete it: %w", filepath.Join(dir, fileName), err)
	}
	if s == nil { // ファイルの中身が null のとき
		s = State{}
	}
	return s, nil
}

// Save は dir に state.json を書く。一時ファイルに書いてから rename するので、
// 途中で落ちても古いファイルは壊れない。dir がなければ作る。
func (s State) Save(dir string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, fileName+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // rename したあとは何もしない
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, fileName))
}

// Prune は keep に含まれない Issue のエントリを消す。tick が、Working State を離れた Issue を消すのに使う。
func (s State) Prune(keep map[string]bool) {
	for id := range s {
		if !keep[id] {
			delete(s, id)
		}
	}
}
