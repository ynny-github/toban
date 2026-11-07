package schedule

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

// Unit は systemd に登録する service と timer の名前（拡張子なし）。1 ユーザーに 1 つだけ登録する（ADR 0002）。
const Unit = "toban"

var service = template.Must(template.New("service").Funcs(template.FuncMap{"q": quote}).Parse(`[Unit]
Description=toban tick

[Service]
Type=oneshot
Environment={{q (print "PATH=" .Path)}}
ExecStart={{q .Toban}} "-config" {{q .ConfigDir}} "tick"
`))

var timer = template.Must(template.New("timer").Parse(`[Unit]
Description=Run toban tick every {{.Seconds}} seconds

[Timer]
OnActiveSec=0
OnUnitInactiveSec={{.Seconds}}s

[Install]
WantedBy=timers.target
`))

// quote は systemd の設定の 1 語として使えるよう、" で囲む。\ と " と % は escape する。
func quote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`).Replace(s)
	return `"` + s + `"`
}

// SystemdUnits は systemd のユーザー単位の service（tick を 1 回走らせる）と timer の定義を返す。
// timer は有効にした直後に 1 回走らせ、その後は tick が終わってから Interval たつごとに走らせる（tick が重ならない）。
// systemd はログインシェルの PATH を引き継がないので、Tools のディレクトリから PATH を作って渡す。ログは journal に出る。
func SystemdUnits(j Job) (svc, tmr []byte) {
	data := map[string]any{
		"Toban": j.Toban, "ConfigDir": j.ConfigDir, "Path": toolPath(j.Tools),
		"Seconds": int(j.Interval.Seconds()),
	}
	var s, t bytes.Buffer
	service.Execute(&s, data)
	timer.Execute(&t, data)
	return s.Bytes(), t.Bytes()
}

// InstallSystemd は unitDir に service と timer を書き、timer を有効にして走らせ直す。何度実行しても 1 つだけになる。
func InstallSystemd(j Job, unitDir string) error {
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return err
	}
	svc, tmr := SystemdUnits(j)
	for name, body := range map[string][]byte{Unit + ".service": svc, Unit + ".timer": tmr} {
		if err := os.WriteFile(filepath.Join(unitDir, name), body, 0o644); err != nil {
			return err
		}
	}
	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", Unit + ".timer"},
		{"restart", Unit + ".timer"}, // 間隔を変えて入れ直したとき、すぐ新しい間隔にするため
	} {
		if out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl --user %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
		}
	}
	return nil
}
