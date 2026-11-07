# Workflow はリポジトリごとではなく Tracker の管理単位ごとに定義し、リポジトリの外に置く

Symphony はリポジトリごとに `WORKFLOW.md` を置くが、Issue をまとめる単位を決めているのは Tracker の側である（GitHub Project は複数のリポジトリの Issue を持てる）。そこで Workflow は Tracker の管理単位（GitHub なら Project）ごとに定義し、Issue がどのリポジトリのものかは Issue ごとに Tracker から得る。Workflow は、利用者が指定する設定のディレクトリ（`-config` か環境変数 `TOBAN_CONFIG`。既定の場所は持たない）の `config.yaml` に置き（置き方は ADR 0007 で `workflows/*.md` から `trackers:` に変えた）、対象リポジトリの中には置かない。これで worker が worktree の中で自分への指示を書き換えて即座に効かせることも起きない。

## Consequences

- リポジトリに固有の指示（ビルド手順など）は、各リポジトリの `CLAUDE.md` や `AGENTS.md` に書く。Workflow のプロンプトには共通の指示だけを書く。
- Workflow の変更はリポジトリの PR で見直されない。設定のディレクトリを専用の Git リポジトリにして、設定の変更をそのリポジトリの PR で見直す。
- worker に渡すプロンプトの置き場所は ADR 0005 で変えた。リポジトリ固有の手順は、対象リポジトリの `WORKFLOW.md` に置く。
