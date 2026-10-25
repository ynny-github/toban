# Domain Docs

エンジニアリング系のスキルが、コードベースを調べるときにこのリポジトリのドメインの文書をどう読むかを定める。

## 調べる前に読むもの

- リポジトリ直下の **`GLOSSARY.md`**
- リポジトリ直下に **`GLOSSARY-MAP.md`** があれば、そちらを読む。コンテキストごとの `GLOSSARY.md` を指しているので、扱う話題に関係するものをそれぞれ読む。
- **`docs/adr/`**: これから手を入れる領域に関わる ADR を読む。multi-context のリポジトリでは、コンテキストに固有の決定がある `src/<context>/docs/adr/` も確かめる。

これらのファイルがなければ、**何も言わずに先へ進む**。ないことを指摘したり、先回りして作るよう勧めたりしない。`/domain-modeling` スキル（`/grill-with-docs` と `/improve-codebase-architecture` から使われる）が、用語や決定が実際に固まった時点で作る。

## ファイルの配置

single-context のリポジトリ（ほとんどのリポジトリ。このリポジトリもこちら）:

```
/
├── GLOSSARY.md
├── docs/adr/
│   ├── 0001-event-sourced-orders.md
│   └── 0002-postgres-for-write-model.md
└── src/
```

multi-context のリポジトリ（直下に `GLOSSARY-MAP.md` がある）:

```
/
├── GLOSSARY-MAP.md
├── docs/adr/                          ← システム全体の決定
└── src/
    ├── ordering/
    │   ├── GLOSSARY.md
    │   └── docs/adr/                  ← コンテキストに固有の決定
    └── billing/
        ├── GLOSSARY.md
        └── docs/adr/
```

## GLOSSARY の語を使う

出力の中でドメインの概念を名指しするとき（Issue のタイトル、リファクタリングの提案、仮説、テスト名など）は、`GLOSSARY.md` で定義された語を使う。GLOSSARY が _Avoid_ に挙げている類義語に流れない。

必要な概念が GLOSSARY にまだなければ、それは合図である。プロジェクトが使っていない言葉を作り出しているか（考え直す）、本当に抜けがあるか（`/domain-modeling` のために書き留める）のどちらかだ。

## ADR との矛盾を明示する

出力が既存の ADR と矛盾するなら、黙って上書きせず、はっきり示す。

> _ADR-0007（event-sourced orders）と矛盾するが、次の理由で見直す価値がある…_
