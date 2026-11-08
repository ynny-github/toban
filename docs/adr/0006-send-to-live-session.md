# Issue の worktree に生きたセッションがあるときは、新しく起動せず、そのセッションに続きの指示を送る

Dispatch は、worker を 1 回起動するだけでなく、すでに生きているセッションへ 1 回送ることも含める。Issue の worktree が見つかったら、`orca worktree ps` の `agents[]` を見て、設定した `agent` の種類の生きたセッション（`hasLivePty`）を数える。0 個なら新しく起動する。2 個以上、または未知の `state` なら、Dispatch を失敗にする。1 個で `state` が `done` なら、そのセッションに送る。`working` なら、State を動かさずに見送り、次の周でやり直す。

送るのは継続の指示だけで、Preamble も Issue の本文も再送しない。どのセッションも同じ Issue の作業をしているはずなので、最初のプロンプトは履歴にあると見なす（Symphony の continuation turn と同じ前提）。Workflow には最初のプロンプトとは別に、続きのプロンプト（`continuation_prompt`）を書く（ADR 0007。当初は真偽値 `Continuation` で本文を出し分けていた）。継続かどうかを知るのは Executor だけなので、tick は両方を展開して `Request` に持たせ、Executor が選ぶ。見送りを State の往復なしにするため、`MoveToWorking` の前に `Executor.Check` を呼ぶ。

送信は `orca terminal send --enter --wait-submit` で行い、`accepted` なら成功とする。受け付けられない、許可メニューが出ている（`agent_prompt_blocked`）、端末が古い（`terminal_handle_stale`）ときは、新しく起動せずに Dispatch を失敗にして State を戻す。1 つの worktree に 2 つのエージェントが動くのを避けるため。toban が起動するセッションは `--surface terminal` に固定し、チャット画面で `terminal send` が届かない事態を避ける。

これは ADR 0001 / 0002 の「起動したら手を離す」を変えない。`worktree ps` を読むのは、新しく起動するか送るかを決める 1 回だけで、worker の進み具合を見張るためには使わない（この点は下の「Working の Issue への継続」で変えた）。

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

## Working の Issue への継続（#12 で追記）

エージェントが返答を終えて止まると、人が State を Ready に戻すまで何も起きなかった。そこで tick は、Dispatch の前に、Working State にある Issue のセッションを見る（Symphony の「正常終了のあと、Issue がまだ active なら worker を付け直す」に当たる）。上の「`worktree ps` を進み具合の見張りに使わない」はここで変わる。

- 対象は、State のほかは Candidate と同じ条件（open、ラベル、assignee、対象リポジトリ）を満たす Working State の Issue。Tracker の `Poll` が `WorkingIssues` として返す。同時実行数の `Working` は、これまでどおり条件によらず数える。
- 生きたセッションが 1 個で `state` が `done` になってから `continue_after`（既定 10 分。`stateStartedAt` から数える）たっていれば、`continuation_prompt` だけを送る。送る経路は上の Dispatch と同じ `terminal send --wait-submit` で、`accepted` なら `state.json` の回数を 1 増やし、`last_sent_at` を記録する。
- 回数が `max_continuations`（既定 5）に達したら、送らずにログに残す。許可メニュー（`agent_prompt_blocked`）のときは「承認待ち」とログに残し、回数は増やさない。受け付けられないときも同じ。
- 生きたセッションがない、`working`、2 個以上、未知の `state` のときは送らない。死んだセッションの起動し直しと、`working` のまま止まったセッションは別の Issue で扱う。
- State は動かさず、Dispatch の枠も使わない。使用量の上限を超えていれば、Dispatch と一緒に見送る。送れなくても tick の失敗にはせず、次の周でやり直す。
- 人の返事待ちで止まっているときは、worker が `WORKFLOW.md` の約束どおり State を Working から動かす。動かさないまま止まると、上限まで継続が届く。
- Working State を離れた Issue のエントリは、すべての Workflow の `Poll` が成功した tick で `state.json` から消す。
