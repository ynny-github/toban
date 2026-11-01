# Issue tracker: GitHub

このリポジトリの Issue と仕様は、GitHub の `ynny-github/toban` の Issues で管理する。操作はすべて `gh` CLI で行う。

## 操作

- **Issue を作る**: `gh issue create --title "..." --body "..."`。本文が複数行になるときは heredoc を使う。
- **Issue を読む**: `gh issue view <number> --json number,title,body,labels,comments`
- **Issue を一覧する**: `gh issue list --state open --json number,title,body,labels,comments --jq '[.[] | {number, title, body, labels: [.labels[].name], comments: [.comments[].body]}]'`。必要に応じて `--label` と `--state` で絞る。
- **Issue を親の sub-issue にする**: `gh issue create --parent <parent> ...`、または作成後に `gh issue edit <parent> --add-sub-issue <child>`（`gh` 2.94 以降）。古い `gh` では `gh api --method POST repos/<owner>/<repo>/issues/<parent>/sub_issues -F sub_issue_id=<child-db-id>`（database id。下の **Blocking** を参照）。sub-issue を使えないときは、子の本文の先頭に `Part of #<parent>` と書く。
- **コメントする**: `gh issue comment <number> --body "..."`
- **ラベルを付ける・外す**: `gh issue edit <number> --add-label "..."` / `--remove-label "..."`
- **close する**: `gh issue close <number> --comment "..."`

リポジトリは `git remote -v` から決まる。clone の中で実行すれば `gh` が自動で判断する。

## PR を triage の対象にするか

**PRs as a request surface: no.** _(このリポジトリが外部からの PR を機能の要望として扱うなら `yes` にする。`/triage` がこのフラグを読む。)_

`yes` のときは、PR も Issue と同じラベルと状態で扱い、対応する `gh pr` のコマンドを使う。

- **PR を読む**: `gh pr view <number> --comments`。差分は `gh pr diff <number>`。
- **triage 対象の外部 PR を一覧する**: `gh api --paginate 'repos/{owner}/{repo}/pulls?state=open' --jq '.[] | select(.author_association | IN("OWNER","MEMBER","COLLABORATOR") | not) | {number, title, author: .user.login, author_association, labels: [.labels[].name]}'`
- **コメント・ラベル・close**: `gh pr comment`、`gh pr edit --add-label`/`--remove-label`、`gh pr close`

GitHub では Issue と PR が同じ番号の並びを使うので、`#42` だけではどちらか分からない。`gh pr view 42` で調べ、なければ `gh issue view 42` で調べる。

## スキルが「Issue tracker に公開する」と言ったとき

GitHub の Issue を作る。

## スキルが「関係する ticket を取ってくる」と言ったとき

上の **Issue を読む** のとおりに読む。

## Wayfinding の操作

`/wayfinder` が使う。**map** は 1 件の Issue で、その **child** の Issue が ticket になる。

- **Map**: `wayfinder:map` ラベルを付けた 1 件の Issue。本文に Notes / Decisions-so-far / Fog を持つ。`gh issue create --label wayfinder:map`
- **Child ticket**: map に GitHub の sub-issue としてつないだ Issue（**Issue を親の sub-issue にする** を参照）。sub-issue を使えないときは、map の本文のタスクリストに子を加え、子の本文の先頭に `Part of #<map>` と書く。ラベルは `wayfinder:<type>`（`research`/`prototype`/`grilling`/`task`）。着手したら、進めている開発者を assignee にする。
- **Blocking**: GitHub の **native issue dependencies** を使う。UI にも表示される正式な表し方である。`gh api --method POST repos/<owner>/<repo>/issues/<child>/dependencies/blocked_by -F issue_id=<blocker-db-id>` で依存を足す。`<blocker-db-id>` は blocker の数値の **database id**（`gh api repos/<owner>/<repo>/issues/<n> --jq .id`）で、`#number` や `node_id` ではない。GitHub は `issue_dependencies_summary.blocked_by`（open の blocker だけを数える、その時点の判定）を返す。dependencies を使えないときは、子の本文の先頭に `Blocked by: #<n>, #<n>` と書く。blocker がすべて close されたら、その ticket は着手できる。
- **Frontier query**: map の open な子を一覧し（`gh issue list --state open` を map の sub-issue かタスクリストに絞る）、open な blocker を持つもの（`issue_dependencies_summary.blocked_by > 0`、または `Blocked by` の行に open な Issue があるもの）と assignee がいるものを除く。map の中で最初に並んでいるものから取る。
- **Claim**: `gh issue edit <n> --add-assignee @me`。そのセッションで最初に行う書き込みにする。
- **Resolve**: `gh issue comment <n> --body "<answer>"`、続けて `gh issue close <n>`。そのあと map の Decisions-so-far に、要点とリンクを書き足す。
