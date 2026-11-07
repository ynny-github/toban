# Issue の worktree に生きたセッションがあるときは、新しく起動せず、そのセッションに続きの指示を送る

Dispatch は、worker を 1 回起動するだけでなく、すでに生きているセッションへ 1 回送ることも含める。Issue の worktree が見つかったら、`orca worktree ps` の `agents[]` を見て、設定した `agent` の種類の生きたセッション（`hasLivePty`）を数える。0 個なら新しく起動する。2 個以上、または未知の `state` なら、Dispatch を失敗にする。1 個で `state` が `done` なら、そのセッションに送る。`working` なら、State を動かさずに見送り、次の周でやり直す。

送るのは継続の指示だけで、Preamble も Issue の本文も再送しない。どのセッションも同じ Issue の作業をしているはずなので、最初のプロンプトは履歴にあると見なす（Symphony の continuation turn と同じ前提）。Workflow には最初のプロンプトとは別に、続きのプロンプト（`continuation_prompt`）を書く（ADR 0007。当初は真偽値 `Continuation` で本文を出し分けていた）。継続かどうかを知るのは Executor だけなので、tick は両方を展開して `Request` に持たせ、Executor が選ぶ。見送りを State の往復なしにするため、`MoveToWorking` の前に `Executor.Check` を呼ぶ。

送信は `orca terminal send --enter --wait-submit` で行い、`accepted` なら成功とする。受け付けられない、許可メニューが出ている（`agent_prompt_blocked`）、端末が古い（`terminal_handle_stale`）ときは、新しく起動せずに Dispatch を失敗にして State を戻す。1 つの worktree に 2 つのエージェントが動くのを避けるため。toban が起動するセッションは `--surface terminal` に固定し、チャット画面で `terminal send` が届かない事態を避ける。

これは ADR 0001 / 0002 の「起動したら手を離す」を変えない。`worktree ps` を読むのは、新しく起動するか送るかを決める 1 回だけで、worker の進み具合を見張るためには使わない。

## Considered Options

- **`terminal wait --for tui-idle` で待ってから送る**：エージェントが考えている最中でも満たされたという報告があり、確実な判断にならないため採らなかった。
- **`working` のセッションにも割り込んで送る**：ターンの途中に別の指示が混ざり、作業が壊れるため採らなかった。
- **送信が失敗したら、新しく起動する**：1 つの worktree に 2 つのエージェントが動くため採らなかった。
- **Preamble も付けて、毎回完全なプロンプトを送る**：履歴にある内容の再送になり、Symphony も採っていないため採らなかった。

## Consequences

- Dispatch のたびに、`worktree ps` と `terminal list` を呼ぶ（新規起動のときは `worktree ps` だけ）。
- 人が別の用事で開いたセッションが worktree にあると、worker が State の移し方を知らず、Issue が Working のまま残る。
- `state` の値は `working` と `done` しか確認していない。それ以外が返る版では、Dispatch が失敗になる。
- チャット画面のセッションには送れず、Dispatch が失敗になる。
