// Package state は toban が実行中に書くファイル（ロック、PID、ログ、tick をまたぐヒントの state.json）を置く state ディレクトリを扱う。
package state

import (
	"os"
	"path/filepath"
)

// Dir は state ディレクトリを返す。override（-state か TOBAN_STATE）、
// $XDG_STATE_HOME/toban、~/.local/state/toban の順に使う。
func Dir(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "toban"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "toban"), nil
}
