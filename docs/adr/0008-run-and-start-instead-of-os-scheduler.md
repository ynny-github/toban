# 定期起動は OS のスケジューラへの登録ではなく、toban 自身のループ（run）と背景起動（start）で行う

ADR 0002 は、tick を `toban schedule install` で launchd や systemd に登録して定期起動する、と決めていた。これをやめて、`toban run`（前面で tick を `interval` ごとに繰り返す）と、それを背景に切り離す `toban start`、止める `toban stop`、様子を見る `toban status` に置き換える。`schedule install` と、launchd と systemd の登録処理は削除した。

tick が状態を持たず、State の真実は Project にあること（ADR 0001）、worker を監督しないこと（ADR 0002）は変えない。`run` は tick を呼ぶだけのループで、claim やリトライタイマーを持たない。

## 理由

- systemd のユーザー timer は、PID 1 が systemd でない環境（コンテナなど）や、ユーザーセッションのない環境では登録できなかった。launchd と systemd の両方の登録処理を持つより、OS に依らず動くほうがよい。
- ADR 0002 が `run` を採らなかった理由は、「どのみち OS に登録するなら、定期起動まで OS に任せたほうが単純」だった。OS への登録をやめるなら、この理由はなくなる。

## 決めたこと

- 置き場所は state ディレクトリ（`-state` か `TOBAN_STATE`、なければ `$XDG_STATE_HOME/toban`、`~/.local/state/toban`）。`run.lock`、`run.pid`、`toban.log` を置く。
- 二重起動の防止と「動いているか」の判断は、`run` が持つ排他ロック（flock）で行う。PID ファイルは `stop` の送り先にだけ使い、信用しない。
- `run` は、周ごとに `config.yaml` を読み直す。間隔を変えても再起動は要らない。始める前の設定の誤りは終了にし、始めたあとの tick の失敗はログに出して次の周に進む。
- `SIGINT` と `SIGTERM` を受けたら、実行中の tick を終えてから止まる。Dispatch の途中で止めて、State と worker の起動が食い違うのを避けるため。
- `start` が起動した `run` のログは `toban.log` に書き、10 MiB を超えたら回転させる（3 世代）。この値は固定で、必要になったら設定にする。

## Consequences

- 落ちた `run` や、マシンの再起動のあとに、toban は自動で立て直さない。`start` をもう一度実行するか、コンテナの起動コマンドや OS 側の仕組みに `toban run` を書く。
- ADR 0002 の「OS ごとの登録処理を toban が持つ」は、なくなる。
- ADR 0002 の「ログインしていないと動かない」制約は、`run` を動かす側（`launchd` や `systemd` を使うかどうか）に移る。
