# オーケストレータは状態を持たない tick として実装し、定期起動は toban が OS のスケジューラに登録する

オーケストレータの役割は Issue を拾って worker を起動するところまでで、起動後の worker を監督しない。待ち受ける受信箱がなく、必要な状態はすべて Tracker の State にあるので、1 周ごとに Tracker を読み直して Dispatch する `toban tick` を定期起動すれば足りる。定期起動は `toban schedule install` が OS のスケジューラ（最初は macOS の launchd のユーザー単位のジョブ）に 1 つだけ登録し、tick はすべての Workflow を順に処理する。間隔はユーザー単位の設定に書き、変えたら `install` をやり直す。同時実行数の上限は Workflow ごとに持ち、全体の上限は持たない。周が重なったときに備えてロックファイルを使う。

## Considered Options

- **worker を監督する常駐デーモン**：worker の完了報告や質問を `orca orchestration check --wait` で待ち受けるなら必要だったが、監督をやめたため採らなかった。
- **toban 自身がループし（`toban run`）、OS には生かし続けることだけを任せる**：間隔を Workflow で変えられるが、どのみち OS に登録するなら定期起動まで OS に任せたほうがコードが単純になるため採らなかった。
- **登録は利用者に任せ、plist の例だけ示す**：toban が OS を知らずに済むが、launchd の `PATH` や shim の扱いで利用者がつまずきやすいため採らなかった。
- **Orca automations で tick を回す**：Orca だけで完結するが、オーケストレータを特定の Executor に依存させることになるため採らなかった。

## Consequences

- OS ごとの登録処理を toban が持つ。最初は launchd だけで、systemd timer は後から足す。
- ユーザー単位のジョブなので、ログインしていないと動かない。Mac mini では自動ログインが前提になる（Orca のデスクトップアプリにも必要）。
- Candidate に気づくまで最大で間隔ぶん遅れる。様子はログと Tracker で見る。
- 将来 worker を監督する役割を足すなら、この決定を見直す必要がある。
