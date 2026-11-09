# closed になってから close_after たった Issue の worktree を、tick が消す

ADR 0002 は、オーケストレータの役割を Issue を拾って worker を起動するところまでとし、起動後の worker には関わらないと決めていた。そのため、Issue が終わっても worktree と Orca の端末は残り続け、人が手で消していた。そこで tick は、closed になってから `close_after` たった Issue（Terminal State）の worktree を消す。これは ADR 0002 の「worker は管理外」の例外になる。worker の作業そのものには手を出さず、終わった Issue の後片付けだけを受け持つ。

## 決めたこと

- 有効にするのは `config.yaml` の `executor.close_after`（例: `30m`）。`0` か省略で無効で、既定は無効。
- 対象は、Workflow の Project に入っていて、対象リポジトリ（`repos`）のもので、closed になってから `close_after` たった Issue。Candidate のラベルや assignee の条件は見ない。閉じたあとにラベルを外していても片付けるため。
- 「いつから closed か」は Issue の `closedAt` で数え、`state.json` には持たない。Project の State（`Done` など）は見ない。再オープンすれば Issue が open に戻るので、対象から自然に外れる。猶予は、誤って閉じたときに作業場所が消えないようにするため。
- 消すのは、Issue に紐付く（`linkedItems` の番号と、`projectId` のリポジトリが合う）worktree すべて。`orca worktree rm --worktree path:<path>` で消し、`--force` は付けない。Orca の記録（端末のタブなど）と git の worktree がまとめて消える。ブランチは残るので、未 push のコミットだけなら失われない。
- 消さないもの
  - `working` のセッションがある worktree（エージェントの種類によらない）。`done` になるまで待つ。未知の `state` の生きたセッションも、同じく残す。`/exit` のあとシェルだけ残ったもの（`programStatus` がない）は落ちているとみなし、待たない（ADR 0006）。
  - メインの worktree（`isMainWorktree`）。
- コミット前の変更（未追跡のファイルを含む）がある worktree は、`worktree rm` が失敗する。`warn` で理由をログに出し、次の tick でまた試す。回数の上限は付けない。tick の失敗にはしない。
- Poll のあと、使用量の上限の確認より前に行う。Claude の使用量を使わないので、上限を超えていても片付けは進める。そのため、使用量の上限の確認は tick の前で止めるのをやめ、Poll のあとで継続と Dispatch だけを見送る形にした。

## Considered Options

- **Project の State（`Done`）で判断する**：State を Done にせず閉じる、Done にしてから再オープンする、といった食い違いが起きる。GLOSSARY の Terminal State は「closed なら値に関係なく」なので、closed を正とした。
- **closed になった時刻を `state.json` に記録する**：GitHub が `closedAt` を持っているので、二重に持つ必要がない（ADR 0001）。
- **`--force` で消す**：コミット前の変更が失われる。人が気づいて片付けられるよう、ログに残して残す。
- **使用量の上限を超えたら片付けも見送る**：使用量と関係のない処理なので、止める理由がない。

## Consequences

- Poll の結果に closed の Issue が入るので、Project にたまった closed の Issue が多いと、応答が大きくなる。取得は前から Project の item を全件読んでいた。
- 有効にすると、tick ごとに `orca worktree list` を 1 回呼ぶ（消す候補があれば `worktree ps` も）。
- コミット前の変更が残った worktree は、人が片付けるまで、tick ごとに `warn` が出続ける。
- 人が同じ Issue に紐付けて手で作った worktree も、`working` でなければ消える。
