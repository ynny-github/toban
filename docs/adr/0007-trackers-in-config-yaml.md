# Workflow は設定のディレクトリの `config.yaml` の `trackers:` に書き、Markdown のファイルは使わない

Workflow は設定のディレクトリの `workflows/*.md`（YAML の front matter とプロンプトの本文）に置いていたが、`config.yaml` の `trackers:` に書く形に変える。ユーザー単位の設定（`interval`、使用量の上限）、Executor（すべての Tracker に共通で、`executor:` を root に置く。省略すると Orca の Claude）、Tracker の管理単位ごとの定義（`trackers:` の 1 件。`name`、`kind` と Tracker の設定、`max_concurrent`、プロンプト）が 1 つのファイルに入る。プロンプトは `prompt` と `continuation_prompt` の 2 つの文字列（Go の `text/template`）に分け、`Continuation` の出し分け（`{{if}}`）をやめる。

## 理由

- Markdown にしていたのは Symphony の `WORKFLOW.md` に形を合わせたため。toban の Workflow の本文は、Issue の URL とリポジトリの `WORKFLOW.md`（ADR 0005）への案内だけで、Markdown として読む場面がない。
- リポジトリの `WORKFLOW.md` と、設定ディレクトリの `workflows/*.md` が同じ名前の並びで、紛らわしかった。
- 設定が `config.yaml` と `workflows/` に分かれていると、ディレクトリの構成を覚える必要があり、`toban init` の雛形も多くなる。

## Consequences

- `workflows/` ディレクトリは読まない。残っていれば tick が警告を出す。
- `config.yaml` に誤りが 1 つでもあれば、tick はどの Project も処理せずにエラーにする。以前は読めない Workflow だけを飛ばしていたが、誤った設定のまま一部だけ動かすのを避ける。
- Workflow の名前（`name`）は必須で、重複できない。ログに出る。
- ADR 0004 の「Workflow ファイルを `workflows/` に置く」は、この ADR で置き換わる。置き場所が設定のディレクトリで、リポジトリの外である点は変わらない。
