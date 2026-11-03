# Workflow はリポジトリごとではなく Tracker の管理単位ごとに定義し、リポジトリの外に置く

Symphony はリポジトリごとに `WORKFLOW.md` を置くが、Issue をまとめる単位を決めているのは Tracker の側である（GitHub Project は複数のリポジトリの Issue を持てる）。そこで Workflow は Tracker の管理単位（GitHub なら Project）ごとに定義し、Issue がどのリポジトリのものかは Issue ごとに Tracker から得る。Workflow ファイルは、利用者が指定する設定のディレクトリ（`-config` か環境変数 `TOBAN_CONFIG`。既定の場所は持たない）の `workflows/` に置き、対象リポジトリの中には置かない。これで worker が worktree の中で自分への指示を書き換えて即座に効かせることも起きない。

## Consequences

- リポジトリに固有の指示（ビルド手順など）は、各リポジトリの `CLAUDE.md` や `AGENTS.md` に書く。Workflow のプロンプトには共通の指示だけを書く。
- Workflow の変更はリポジトリの PR で見直されない。設定のディレクトリを専用の Git リポジトリにして、設定の変更をそのリポジトリの PR で見直す。
