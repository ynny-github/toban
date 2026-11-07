# worker への手順は対象リポジトリの WORKFLOW.md に置き、toban は読まずに存在だけを Dispatch の前に確かめる

worker に渡すプロンプトは、Tracker が作る前置き（State の移し方と ID）、Issue の URL と本文、「このリポジトリのルートの `WORKFLOW.md` に沿って進める」という短い指示だけにする。ビルドや PR の出し方などの手順は、対象リポジトリのルートの `WORKFLOW.md` に書き、worker が worktree の中で自分で読む。toban は `WORKFLOW.md` を読まず、解釈もしない。そのため、取得するブランチや書き換えの扱いを toban が決める必要がない。手順の変更はコードと同じ PR でレビューされる。

ただし、手順書のないリポジトリで worker を起動すると、worker が自分の判断で進めてしまう。そこで Dispatch の前に、Issue のリポジトリのデフォルトブランチにファイルがあることを確かめ、なければ State を動かさずに Dispatch を失敗させる。API のエラーで確かめられないときも同じ扱いにする。

Project 単位の設定（Candidate の条件、`max_concurrent`、Executor の設定）は、これまでどおり設定のディレクトリの Workflow に置く（ADR 0004）。Issue を探すには先に Project の URL が要るので、これはリポジトリの外にしか置けない。

## Considered Options

- **toban が各リポジトリの `WORKFLOW.md` を取得してプロンプトに展開する**：ブランチの選び方、取得の失敗、書き換えの扱いを toban が持つことになるため採らなかった。
- **ファイルがなければ、worker にコメントさせて止める**：起動と State の移動が済んでしまい、Working State の枠を使う。起動の前に弾くほうが、State が実態を表すという ADR 0001 に合うため採らなかった。
- **存在確認をしない**：手順のないリポジトリで、worker が意図しない作業をするため採らなかった。

## Consequences

- Dispatch のたびに GitHub API を 1 回呼ぶ。
- 失敗した Issue は、枠を使わず次の Candidate に進む。毎周ログにエラーが残るので、利用者は `WORKFLOW.md` を足すか、ラベルを外す。
- State を移したあとの失敗（Tracker や Executor の不調）は、その周の残りの Candidate を試さずに止める。
- `WORKFLOW.md` の中身が空や不正でも、toban は検知しない。
- `WORKFLOW.md` はリポジトリの worker 向けの手順書で、Workflow（Project 単位の定義）とは別物である。
