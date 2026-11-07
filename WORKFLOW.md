# WORKFLOW

toban が起動した worker が、Issue を処理するときに従う手順。

この Issue は、grilling で固めた内容を `/to-spec` と `/to-tickets` でチケットにしたもの。本文に仕様と受け入れ条件があり、先に終わらせるべき Issue は `Blocked by` に書かれている。実装は `implement` スキルで進める。

## 手順

1. **Issue を読む。** `gh issue view <number> --json number,title,body,labels,comments` で本文とコメントを読み、タイトルを述べてから始める。`Blocked by` の Issue に open のものがあれば、実装せず「進められないときの扱い」に従う。
2. **前提を読む。** `GLOSSARY.md` と、手を入れる領域に関わる `docs/adr/` を読む。名前や説明には GLOSSARY の語を使い、ADR と矛盾する場合は黙って上書きせず、PR にその旨を書く。
3. **`implement` スキルで実装する。** Skill ツールで呼べないときは、スキルの `SKILL.md` を読んで同じ手順で進める。
   - 合意されたテストの境界で `tdd` を使う。
   - 終わったら `code-review` で確認する。
   - Issue に書かれていない変更はしない。
4. **コミットする。** `.claude/rules/git-commit.md` に従う。フッターは `Fixes: #<number>` にする。
5. **PR を作る。** `create-pr` スキルを使う。main に直接 push しない。
6. **Review に移す。** 最初に渡された `gh project item-edit` のコマンドで、この Issue の State を Review にする。

## 進められないときの扱い

仕様が足りない、blocker が open、環境の問題などで続けられないときは、次の 3 つを行って止まる。

1. 何が足りないかを、Issue にコメントする。
2. `ready-for-agent` ラベルを外し、`needs-info` ラベルを付ける。
3. State を Todo に戻す。

ラベルを外すのは、そのままでは次の tick で同じ Issue がまた Dispatch されるため。
