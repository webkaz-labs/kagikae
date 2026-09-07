# 認証の再確認・CI・製品説明

## 到達点

現行機能の信頼性と保守の自動化を、複数テーマをまとめたリリースで改善する。
採用差分が内部リファクタ・検証・説明修正に収まる場合の候補版は v0.19.1。
ユーザー承認の下、条件付き項目の採否を判断し、実装から公開まで進める。

## 判断と作業の台帳

| 作業 | 状態 | 採用・完了条件 | 依存 |
|---|---|---|---|
| リリース範囲 | 承認済み | 本表の必須・条件付き項目を対象に実装・公開する | なし |
| README の保証範囲 | 完了 | 認証の保存と有効性、global rollback と original-store preservation を混同させない説明にする。冒頭と安全性の紹介に限定し、詳細は既存契約へ参照する | 範囲確定 |
| formatter の CI 差 | 検出目的で採用 | Linux の故障対照を確認し、既存 job の gofmt をローカルの formatter 検査へ置換する。速度改善とは扱わない | 範囲確定 |
| docs selftest の CI 配置 | 検出目的で採用 | Linux の故障対照を確認し、既存 job に追加する。formatter の採否とは独立で、速度改善とは扱わない | 範囲確定 |
| credential の観測・再確認 | 実装・レビュー完了 | mapping 再解決・存在・raw bytes 比較を cmd 内の非公開 module にまとめる。初回の期待値照合・lock・保存・操作の位置と操作別エラーは caller に残し、既存の command controls で検証する | 方針コミット後に実装 |
| build cache の分離費用 | 今回見送り | gate に共用を導入する根拠となる compilation 重複の測定がないため変更しない。再開条件は同一条件で重複と全体時間を測定できること | 必須 gate の測定 |
| 統合検証・レビュー | 完了 | 採用差分に対応する gate、正確性レビュー、独立品質レビュー、文書判定が完了する | 採用実装 |
| リリース受入・公開 | 実機受入済み・公開待ち | 影響する受入と公開前検査、main CI、公開、配布物検証を完了する | 統合検証・公開指示 |

条件付き項目の不採用は、根拠と再開条件をその行に残して閉じる。
作業数を増やすために新しい抽象化や検査を追加しない。

2026-09-07 の Linux workflow（candidate `e158087`）で、formatter は初回
13.16 秒、同一 job の再実行は 0.74 秒、cache は 136872 KiB だった。docs
selftest は 10.51 秒、5.62 秒、cache は 46048 KiB で、24 ケースを
`GOPROXY=off` と空の module cache で実行した。両方とも速度改善ではなく検出目的で
採用する。再実行は同一 job 内であり、job 間の cache 復元効果は測定していない。
formatter の対照は unused import と空行の検出を示し、local import grouping は
検証していない。これらは gate 全体の速度比較ではない。CI 費用が検出利益に
見合わなくなった場合は採否を再検討する。測定記録: [Linux workflow run](https://github.com/webkaz-labs/kagikae/actions/runs/34085892981)。

## 候補の根拠と停止条件

README の説明は [CLI.md](../CLI.md) § kae preservation Semantics、
[CREDENTIAL-RULES.md](../CREDENTIAL-RULES.md)、
[PRODUCT.md](../PRODUCT.md) を基準にする。保存は token validity の保証ではない。
全ドキュメントの清掃や新しい prose verifier はこの作業に含めない。

CI の比較対象は [mise.toml](../../mise.toml) の fmt-check と
[check.yml](../../.github/workflows/check.yml)、docs selftest は
[check-docs-selftest.sh](../../scripts/check-docs-selftest.sh)。macOS の過去の値を
Linux CI の費用とみなさない。時間削減を主張するときは同じ条件の gate 全体で
比較し、工程単体が速くても全体が改善しなければ高速化として採用しない。
formatter を CI に追加する検出利益と、速度改善は別々に評価する。

認証候補は [preservation.go](../../internal/cmd/preservation.go) と
[relogin.go](../../internal/cmd/relogin.go) の観測・再確認に限定する。
quota・retention を所有する preservation module は作り直さない。
lock の取得順と保持期間、dry-run、保存と実際の変更の位置は維持する。
policy フラグ、callback 群、新 package を必要とする案や、順序知識が caller に
残るだけの forwarding helper は採用しない。共通化だけで再確認の呼び忘れが
防げるとは扱わず、command の統合テストを残す。細かな helper のテストを
既存テストに上乗せすること自体を成果にしない。

## 実行順と検証

実装は最大二系列とする。認証候補の比較と CI の費用・対照調査は並行できる。
README の整理は独立して進め、共有 workflow と文書の編集は統合担当が持つ。
CI 費用が高い項目や弱いリファクタ候補を待って、完成したテーマの公開を
無期限に遅らせない。未知の製品判断が出た場合はその候補だけを切り離す。

Luna は範囲を限定した棚卸し、表記・リンク確認、機械的な編集、文書の品質レビューに
使う。認証の設計・失敗順序の判断、候補の反証は上位モデルが担当し、検証コマンドと
採否・統合ゲートは親が持つ。Luna の結果も成果物で確認し、不確実な判断は引き上げる。

コミット前の gate は [AGENTS.md](../../AGENTS.md) § Validation に従う。
実装・CI 変更には full gate、説明・測定記録だけには docs-check と差分検査を
使う。必要な故障対照は対象 consumer に限定し、変更のない実装の gate や
レビューは説明更新だけを理由に繰り返さない。smoke と checkout 編集は直列化する。

認証 candidate の変更は [ACCEPTANCE.md](../ACCEPTANCE.md) の影響する
実機受入を行う。開始前に対象 session の停止を確認する。既存 v0.19.0 の結果を
新 candidate の実測と呼ばない。公開前後の手順は [RELEASE.md](../RELEASE.md)
を使い、別の手動手順を増やさない。

## 今回含めない判断

- 新しい認証保全対象、保持数・容量上限、復元先の変更。
- stale identity の帰属、移動した reader、未知 payload の上書き、PinID migration。
  再開条件は [ROADMAP.md](../ROADMAP.md) § Current work order に従う。
- account lifecycle の共通化と検査削除の再検討。
  [既存の採否](verification-efficiency.md) を再利用する。
- Python 一律移植、汎用キャッシュ基盤、全シェル補完の作り直し。
- Windows、TUI、対応ツール拡張、外部の shared skill や生成済みグローバル設定。
