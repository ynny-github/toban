// Package schedule は toban tick を定期的に起動するジョブを OS のスケジューラに登録する。
package schedule

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"text/template"
	"time"
)

// Label は launchd に登録するジョブの名前。1 ユーザーに 1 つだけ登録する（ADR 0002）。
const Label = "dev.toban"

type Job struct {
	Toban     string        // toban の実行ファイルの絶対パス
	ConfigDir string        // 設定のディレクトリ
	Tools     []string      // tick が使うコマンド（gh、orca など）の絶対パス。PATH はこれらのディレクトリから作る
	Interval  time.Duration // tick の間隔
	Log       string        // ログの出力先
}

var plist = template.Must(template.New("plist").Funcs(template.FuncMap{"x": escape}).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{x .Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{x .Toban}}</string>
		<string>-config</string>
		<string>{{x .ConfigDir}}</string>
		<string>tick</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>{{x .Path}}</string>
	</dict>
	<key>StartInterval</key>
	<integer>{{.Seconds}}</integer>
	<key>RunAtLoad</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{x .Log}}</string>
	<key>StandardErrorPath</key>
	<string>{{x .Log}}</string>
</dict>
</plist>
`))

// Plist は launchd のユーザー単位のジョブの定義を返す。
// launchd はログインシェルの PATH を引き継がないので、Tools のディレクトリから PATH を作って渡す。
func Plist(j Job) []byte {
	var dirs []string
	for _, t := range j.Tools {
		if d := filepath.Dir(t); !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	dirs = append(dirs, "/usr/bin", "/bin")
	var b bytes.Buffer
	plist.Execute(&b, map[string]any{
		"Label": Label, "Toban": j.Toban, "ConfigDir": j.ConfigDir, "Log": j.Log,
		"Path": strings.Join(dirs, ":"), "Seconds": int(j.Interval.Seconds()),
	})
	return b.Bytes()
}

func escape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// Install は plist を plistPath に書き、launchd のジョブを入れ替える。何度実行しても 1 つだけになる。
func Install(j Job, plistPath string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("schedule install supports only macOS (launchd) for now")
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(j.Log), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, Plist(j), 0o644); err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain+"/"+Label).Run() // 未登録ならエラーになるが、それでよい
	if out, err := exec.Command("launchctl", "bootstrap", domain, plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap %s: %w: %s", plistPath, err, bytes.TrimSpace(out))
	}
	return nil
}
