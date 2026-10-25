# Issue の状態は Project の State だけを正本とし、Dispatch の記録も State で残す

Issue の進み具合は GitHub Project の State だけで表し、オーケストレータは永続 DB を持たない。オーケストレータは worker を起動したあとには関わらないため、「この Issue はもう Dispatch した」という事実を確実に残せるのはオーケストレータ自身しかいない。そこで Dispatch のときだけ、オーケストレータが State を書き換える。先に State を Working State に移し、成功したら worker を起動する。起動に失敗したら元の State に戻す。それ以外の State の変更（レビュー待ちへの移動、終了など）は、エージェントと人が行う。

## Considered Options

- **オーケストレータは State を一切書かず、メモリ上の claim で二重 Dispatch を防ぐ**：オーケストレータが worker を監督し続ける設計なら成り立つが、起動したら手を離す設計では、再起動や tick をまたいで claim を覚えておけないため採らなかった。
- **SQLite などに Dispatch 履歴を持つ**：正本が 2 つになり、食い違いの解消が必要になるため採らなかった。
- **Executor 側（Orca の worktree と issue の紐付け）を見て Dispatch 済みか判断する**：Executor ごとに判断の仕方が変わり、Tracker だけで完結しなくなるため採らなかった。

## Consequences

- State を移したあと起動前にオーケストレータが落ちると、worker のいない Issue が Working State に残る。人が気づいて State を戻す必要がある。
- 同時実行数は Working State にある Issue の数で数えるので、人が worker と話している間やエージェントが State を移し忘れている間も 1 件と数える。
