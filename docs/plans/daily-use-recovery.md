# 日常利用・復旧導線の計画

## 到達点

v0.19.2 の候補として、日常利用で不足する説明と、既存 command の信頼性を
小さな差分で改善する。新しい JSON field・command・認証 policy は追加しない。
候補の判断と実装はユーザー承認後に開始する。

この計画の判断入口はこの表とする。ROADMAP には索引だけを置き、別の issue
階層や新しい glossary/ADR は作らない。候補版に新しい契約が必要になったら、
この計画を保留して scope を再合意する。

## 優先順位と作業表

| ID | テーマ・状態 | 既存機構と最小差分 | 完了条件・依存 |
|---|---|---|---|
| A | README 導線（提案） | README の初回導入、更新、診断、復旧、削除説明を補う。既存の `init`、`add`、`profile set/save`、`doctor`、`backup`、`preservation`、completion、install script を再利用する | README の実行例が fresh state で順序どおり動く説明になり、秘密・identity・絶対パスを公開しない診断手順を示す。B/C と独立して着手可 |
| B | profile 設定競合（提案・第一優先） | `profile.go:206`/`:285` のキャッシュ済み profile 判定と `app.go:434` の config 編集 seam を、既存 `config.Editor` の lock 内最新再読に合わせる。全 account lifecycle の共通化はしない | stale な `App` を二つ使う interleaving で、新しい mapping/default を消さず、通常更新と no-write refusal、`--force`、`--dry-run` を保つ。B/C は App/config seam が重なるため担当を調整 |
| C | preservation list の診断可用性（提案） | backend 選択から独立して既存 `preservation.Store.List` を使う。既存の metadata 表示を保ち、restore/rm の順序・契約は変更しない | backend 選択が失敗する環境でも一覧が成功し、payload を読まない。空一覧・不完全 metadata の既存扱いと JSON shape を維持する。B と編集先を調整 |

実装系列は A と B/C の最大二系列とする。A は README のみ、B/C は担当を
分けるが、同じ `App` seam を触る変更を並行編集しない。第一優先は B、次に A、
C の順で判断する。C は runtime proof を確認済みだが、実装採用はユーザー承認後に判断する。

## A: README の利用導線

初回導入は `init` → `add claude main` / `add claude side` →
`profile set main claude main`（side も必要なら明示的に set）→ `use main` の
経路を示す。`profile save` は active account が存在してから使う説明に限定し、
fresh state の直後には置かない（現状は active account がないと exit 7）。

Install source ごとに `~/.local/bin` の PATH と `command -v kae`/`kae version`、
installer の `--version`、mise の更新、completion refresh を説明する。ローカル
build は既存の `kae completion --refresh` を使う。

診断は `kae doctor`、`kae status --json`、`kae version`、終了コードと
stderr を最小入口にする。raw JSON の公開添付は勧めない。status/accounts の
identity/email、preservation list の directory、account/record ID、個人パスを
確認して匿名化する。bound directory の問題には tool を付けない doctor を案内し、
必要な項目を確認してから Issue に共有する。support bundle や自動匿名化は作らない。

復旧は global backup と original-store preservation を分ける。`backup list` →
通常の `rollback` または明示 `rollback --to <id>`、preservation は
`preservation list/restore <id>` とする。uninstall は binary の削除だけを案内し、
binding、hooks、config、snapshot、preserved credential は残ることを明記する。
binary を外す前に、既存 binding/hook が kae を呼ぶ可能性と解除先を案内する。
破壊的な userdata wipe と self-update command は今回扱わない。
README は短い入口にし、詳しい契約は既存 CLI 文書へ参照する。

## B: profile の stale config 修正

既存の proof は `/tmp` の overlay を commit `0f61c78` で 2026-09-07 に確認済み。
`unset` で新しい mapping を失う形、`rm` で新しい default を無効にする形、fresh
save の exit 7 が再現された。実 credential や利用中の設定は使っていない。

実装では lock を取った後に config を再読し、最新判断を `config.Editor` に渡す。
読み取り前の cached decision を lock 後も使わない。既存の `force`、dry-run、
default の安全な拒否、comment-preserving write を維持する。

受入対照は stale App の二つの interleaving、通常の save/set/unset/rm、default
削除の refusal/force、dry-run、同時変更を消さない no-write を含める。一般的な
account lifecycle redesign、別の config writer、広い retry policy は含めない。

## C: preservation list

`Store.List` は record metadata を読めるが、現在の `runPreservation` は backend
選択を先に行う（`internal/cmd/preservation.go`）。Go overlay の
`TestPlanningPreservationListBackendSelectionFailure` を commit `0f61c78` の
Linux を模した fixture で確認した結果、configured keychain の選択が不整合でも
`Store.List` は backend nil のまま metadata を 1 件返せる一方、CLI の list は
exit `9` になった。この証拠は backend 選択失敗に限り、macOS の locked keychain
全般を示さない。実装採用はユーザー承認後に行い、現状の JSON shape と
restore/rm を維持する。

## 検証とリリース境界

説明のみなら `docs-check` と差分検査、実行例の変更は該当 consumer の確認も行う。
初回手順の認証部分は synthetic fixture に置換し、既存 smoke runner で順序を確認する。
B/C の実装は対象 control を先に行い、各コミットの gate は
[AGENTS.md](../../AGENTS.md) § Validation に従う。修正箇所の正確性レビュー後に
独立品質レビューを行い、指摘修正があれば影響範囲の正確性を再確認する。
[RELEASE.md](../RELEASE.md) が要求する公開前後の検査は実施する。
[ACCEPTANCE.md](../ACCEPTANCE.md) の実機受入は実装の影響から要否を判定し、
credential write path に影響がなければ同じ実機手順を慣例だけで繰り返さない。

今回は quota/retention の再設計、filtered doctor の契約変更、support bundle、
self-update、uninstall の userdata wipe、Windows、TUI、tool 拡張、保護された
authentication research を含めない。新しい evidence が scope を広げる場合は
この計画を停止し、先に判断を更新する。
