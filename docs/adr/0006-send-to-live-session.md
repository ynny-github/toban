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
- 生きたセッションがない、`working`（下の停滞を除く）、2 個以上、未知の `state` のときは送らない。死んだセッションの起動し直しは別の Issue で扱う。
- State は動かさず、Dispatch の枠も使わない。使用量の上限を超えていれば、Dispatch と一緒に見送る。送れなくても tick の失敗にはせず、次の周でやり直す。
- 人の返事待ちで止まっているときは、worker が `WORKFLOW.md` の約束どおり State を Working から動かす。動かさないまま止まると、上限まで継続が届く。
- Working State を離れた Issue のエントリは、すべての Workflow の `Poll` が成功した tick で `state.json` から消す。

## working のまま動かないセッションへの継続（#14 で追記）

`working` のまま長く動かない（停滞した）セッションにも、`done` と同じように `continuation_prompt` を送る。違うのは、動きがない時間の測り方だけ。

- 動きがない時間は `now - max(agents[].updatedAt, lastOutputAt)`（どちらも Issue の worktree のもの）。Orca 1.4.243 では、`updatedAt` はツール呼び出しのたびに、`lastOutputAt` は出力のたびに更新される。
- `stall_after`（既定 30 分。`0` で送らない）を超えたら送る。回数の上限 `max_continuations`、`state.json` への記録、承認待ちの扱い、State と Dispatch の枠を使わないこと、使用量の上限で見送ることは、`done` の継続と共通。前に送ったあと動きがなければ、同じ停滞に重ねて送らない。
- 上の「`working` のセッションに割り込んで送る」を採らなかった理由は、ここには当たらない。`working` 中のセッションに `terminal send` で送った文字は割り込まずに積まれ、実行中のツールが終わったあとの次のターンで処理される（実機で確認）。受信確認は `input_accepted` までで `turn_started` は付かないので、これまでどおり `accepted` で受け付けを判定する。Dispatch は、これまでどおり `working` のセッションに送らない。

### Consequences

- バックグラウンドの処理を待っているだけのセッションも、`stall_after` を超えると継続が届く。積まれるだけなので害は小さいが、送った回数が上限に近づく。
- Claude が終了してシェルだけが残ると、`worktree ps` は `working` のままになり、継続の文章がシェルに入力される。起動し直しは #15 で扱う。

## 落ちたセッションの起動し直し（#15 で追記）

Working State の Issue に生きたエージェントがいない（worker が落ちた）ときは、継続の代わりに、最初のプロンプト（Preamble と本文）で起動し直す（Symphony の「異常終了のあとの、バックオフ付きリトライ」に当たる）。State は Working のままにし、Dispatch の枠も使わない。

- 「落ちた」は、Issue の worktree があり、設定した `agent` の種類の生きたセッションが 0 個のこと。生きたセッションの条件に、`hasLivePty` に加えて `programStatus` があることを足した。Orca 1.4.243 では、端末を閉じると `agents` が空になり、Claude を `/exit` で終えてシェルだけが残ると `state=done`、`hasLivePty=true` のまま `programStatus` が消える。`programStatus` がない `working`（再起動前の古いセッションで見た）も落ちたとみなす。
- この条件は Dispatch にも効く。Ready に戻された Issue の worktree にシェルだけが残っていれば、そこへ継続を送らず、新しく起動する。
- 起動し直す経路は Dispatch と同じ `Executor.Dispatch`（worktree があり、生きたセッションがなければ `agent launch`）。
- 待ち時間は `relaunch_after`（既定 10 分）で、起動し直すたびに倍にする（10 分、20 分、40 分）。worktree で最後に動いた時刻（`max(agents[].updatedAt, lastOutputAt)`）と、`state.json` の `last_sent_at` の遅いほうから数える。どちらも分からなければ、すぐ起動し直す。
- 回数の上限は `max_relaunches`（既定 3。`0` で起動し直さない）。起動に成功したら `state.json` の `relaunches` を 1 増やし、`last_sent_at` を記録する。上限に達したら、ログに残すだけにする。起動に失敗しても回数は増やさず、tick の失敗にもせず、次の周でやり直す。
- 回数は継続（`continuations`）とは別に数え、Working State を離れたら継続と一緒に消える。使用量の上限を超えていれば、Dispatch や継続と一緒に見送る。

### Consequences

- 上の「Claude が終了してシェルだけが残ると、継続の文章がシェルに入力される」は起きなくなる。シェルは残ったまま、別の端末で claude を起動する。
- `programStatus` を目印にするのは 1 回の観測にもとづく。Orca がこれを出さない版では、すべてのセッションが落ちたとみなされ、生きたセッションがあっても起動し直してしまう。
- Claude をシグナルで強制終了したときの見え方は確かめていない（`/exit` と同じになると想定している）。
