# toban

トラッカー上の Issue を起点に、コーディングエージェントを自動で起動して作業を引き渡すオーケストレータ。起動したあとの作業は worker と人に任せる。トラッカーと実行側はどちらも差し替えられる。

## Language

**Tracker**:
Issue と State を持っている外部のシステム（GitHub Project、Linear、Jira など）。
_Avoid_: プロバイダ、チケットシステム

**Executor**:
Dispatch された worker を実際に起動する実行側（Orca、`claude -p` など）。作業場所の確保も Executor が受け持つ。
_Avoid_: Runner、バックエンド、エージェント

**Adapter**:
ユーザーが用意し、公開されたプロトコルで Tracker や Executor をオーケストレータにつなぐ外部プロセス。組み込みの Tracker や Executor は Adapter ではない。
_Avoid_: プラグイン、ドライバ、連携

**Issue**:
トラッカー上の 1 件の作業項目で、オーケストレータが作業を引き渡す単位。GitHub・Linear・Jira などプロバイダによらずこの名前で呼ぶ。
_Avoid_: チケット、タスク、Task

**Project**:
Issue の状態を管理する GitHub Project。Issue は Project に入っていて初めて管理対象になる。
_Avoid_: ボード、カンバン

**State**:
Project 上で Issue が今いる段階（`Todo`、`In Progress`、`Review`、`Done` など）。Issue が closed なら、値に関係なく Terminal State とみなす。
_Avoid_: ステータス、ラベル

**Ready State**:
Dispatch を待っている State。Candidate はこの State からだけ選ぶ。
_Avoid_: Active State、着手可能、Todo

**Working State**:
Dispatch されて作業中の State。この State にある Issue の数を同時実行数として数える。
_Avoid_: 実行中、In Progress

**Terminal State**:
Issue の作業が終わったことを表す State。
_Avoid_: 完了、クローズ

**Candidate**:
Dispatch してよい条件をすべて満たす Issue。Workflow の Project に入っていて、Ready State にあり、指定のラベルと assignee を持つものを指す。対象リポジトリが指定されていれば、そのリポジトリのものに限る。
_Avoid_: 対象 Issue、eligible

**Dispatch**:
Candidate の State を Working State に移し、その Issue のための worker を 1 回起動すること。起動したあとの worker には関わらない。
_Avoid_: Attempt、Run、起動、割り当て

**Workflow**:
Tracker 上の 1 つの管理単位（GitHub なら Project）について、Candidate の条件、Executor の設定、worker に渡すプロンプトのテンプレートをまとめた定義。1 つの Workflow が複数のリポジトリの Issue を扱える。
_Avoid_: 設定ファイル、プロンプト、リポジトリ設定
