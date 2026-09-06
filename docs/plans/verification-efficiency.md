# 必要な検証と作業工程への整理

## 到達点（v0.18.4 で完了）

現行機能の信頼性を維持しながら、変更を検証して届けるまでの不要な作業と
待ち時間を減らす。必要性の棚卸し、実行タイミングと重複の整理、残した検証の
高速化、自動化・CI 配置の順で進める。この優先順位はユーザーと合意済み。
検査数やタスク件数ではなく、必要な不具合を検出できることと、反復作業の削減で
完了を判断する。account lifecycle の構造改善は、この主系列を妨げない範囲で
調査を並行し、実装は入口判断で不採用とした。以下は実施した計画と採否の記録。

このファイルがタスク状態と判断の台帳。[ROADMAP.md](../ROADMAP.md) は索引を持つ。
v0.18.4 の公開結果と手順は [RELEASE.md](../RELEASE.md) に位置付ける。
新しい tracker、変更分類器、汎用 mutation framework は追加しない。

## 作業順と完了条件

| ID・状態 | 作業 | 依存 | 完了の証拠 |
|---|---|---|---|
| N1 完了 | 検査と作業工程の必要性を棚卸しする | なし | 下表の各候補について、検出する故障、固有の価値、代替可能性、実行条件を判定。残す・統合・移動・削除の根拠を記録する。初期候補の発見を削除の証拠とはしない。 |
| N2 完了 | 編集時・コミット前・関連変更時・リリース時の実行条件とレビュー範囲を整理する | N1 の対象別判断 | 純粋な説明変更と実行される文書を区別。正確性・独立品質の二段階と指摘解消確認を維持し、未変更部分の全面再確認を減らす。PJ 内の規約と手順を同期する。 |
| N3 完了（維持） | 重複する実行を統合する | N1 の対象別判断 | analyzer の版・設定・検出範囲や fixture の違いを確認し、残した経路が必要な失敗対照を検出する。共通部分と固有部分を混同しない。 |
| P1 完了 | 残る検証を計測し、時間が集中する処理を改善する | N2、N3 | 各 subtask を単独で測定し、上位二つの内訳を調べる。前後で同条件の時間と検出能力を比較。無効な最適化は数値と不採用理由をこの台帳に残す。 |
| A1 完了 | smoke の一時 HOME 所有と回収を整理する | N1、P1 の対象判断 | 既存 harness と naming 検証の知識を再利用。成功・失敗時の回収、所有外を削除しない対照、従来の隔離を確認する。caller shell に終了 trap を追加しない。 |
| A2 完了 | 毎回組み直す配布物・installer 検証を保存する | P1。既存成果物の棚卸しは N1 後に先行可 | 既存の取得・checksum・attestation・native version・隔離 installer 検証を再利用。不足、破損、証明失敗、未実行を成功と区別する。公開操作とは分ける。 |
| A3 完了 | completion・store の隔離検証を再実行可能にする | N1、A1 | 既存の normative block とテストにない実行経路だけを保存。既存 smoke-run を使い、利用できない shell を成功扱いしない。実機認証の代替とはしない。 |
| C1 完了（既存 CI を利用） | CI に置く検査を工程別に選定する | N2、N3、P1 | 時間、cache、network、書込先、検出範囲を比較。採用工程だけを追加し、据置にも根拠を残す。ローカル gate の無条件コピーはしない。 |
| R1 完了 | account mutation lifecycle の characterization と設計判断 | N1 と並行可 | rm と rename の lock、再読、secret-ref 検証、config/state 更新、途中失敗と再試行の違いを固定。単なる移動や lock helper なら不採用。 |
| R2 完了（不採用） | account mutation lifecycle を深める | R1 で利益を確認 | 既存の拒否・更新順序・回復動作を保ち、既存の競合・失敗対照が command と同じ seam を通る。新しい lock や rollback policy は加えない。 |
| I1 完了 | 統合・文書更新・リリース判断 | 採用した上記作業 | 残した検査の検出範囲、変更前後の実行回数・時間、二種のレビュー結果を確認。実装に対応する必須 release 検証を実行し、scope と版を決める。 |

## 初期監査と判断に必要な証拠

2026-09-07、v0.18.3 後の tree を読んだ結果。以下は候補であり、まだ検査の削除・
移動を許可する結論ではない。実行定義の正本は `mise.toml` と
`.github/workflows/check.yml`。下表はそれらの一覧を複製しない。

| 対象 | 初期所見 | 判定に必要な証拠 |
|---|---|---|
| 独立した vet / Staticcheck と golangci-lint | `.golangci.yml` でも govet と staticcheck を有効化している | analyzer の版・有効規則・設定を比較し、代表的な違反が統合後も検出されるか確認 |
| build と Go test | compilation が重なる候補 | main の link、build tags、platform、test-only code を含む差を確認。release cross-build は別の証拠 |
| smoke selftest と fast mutation | mutation 内にも無変更の baseline selftest がある | fixture baseline と実 checkout の検査範囲を比較。info/exclude の復元と漏れ検出が同じとは仮定しない |
| docs-check とその selftest | 前者は現在の tree、後者は検査器の故障を検出する | checker、docrefs、fixture、Markdown anchor、tracked inventory の依存を調べ、条件付き実行の取り漏らしを確認 |
| 文書変更後の full gate | 説明・状態更新と、実行される文書が同じ運用になっている | Markdown 拡張子だけで判断しない。実行 block、コードが読む表、規約・検証手順は影響先の検証を行う。影響不明なら full gate |
| レビューの反復 | 指摘解消後に未変更部分を全面的に読み直す余地がある | 二段階のレビューを維持し、修正箇所と影響先を再確認する運用へ。検出した指摘を未確認で閉じない |
| 文書 sweep・各ファイル判定 | 同じ作業中に判定を取り直している可能性 | 作業単位の判定を再利用し、影響が増えた場合だけ追加。移動・削除時の inbound reference 確認は保持 |
| 公開後・隔離 smoke の手作業 | 前回は一時 script から配布物・installer・completion・store を検証した | 一時成果物と既存の保存済み検証を比較し、不足する入口だけを恒久化 |

Go product tests、検査器の selftest、mutation、upstream 実測は違う失敗を検出する。
名前や似た出力だけでは重複と判定しない。テストを残すためだけの新しいテストや、
実装の書き写しになる assertion を増やさない。

## 採否の記録

- **N1 / N3 — analyzer と build を維持（2026-09-07）。** 隔離した不正
  format fixture は vet と standalone Staticcheck の両方が拒否した一方、
  boolean 比較の S1002 は現行 golangci-lint が独自に検出した。golangci 側は
  honnef v0.7.0、standalone は v0.8.1 の SA 系であり、同じ集合ではない。
  govet も embedded と Go toolchain で analyzer 集合が異なる。
  main 関数のない package main は `go test ./...` が成功し `go build ./...`
  が失敗したため、build も残す。重なる例一つを根拠に経路全体を削除しない。
- **N1 — verifier の対照は維持。** docs-check は現在の tree、その selftest は
  検査器の故障を検出する。fast mutation 内の baseline は fixture 上であり、
  実 checkout の selftest と同等と確認できていない。認証の保護を担う product
  tests と、実機への漏れ・false-green を検出する verifier tests は代替しない。
- **R1 / R2 — account lifecycle 共通化を不採用（2026-09-07）。**
  removal は preflight から config edit / state save まで state lock を保持し、
  rename は preflight 後に解放して copy 後に再取得する。config 不在時の扱いと
  cleanup 失敗後の再試行も異なる。既存 report builder を通る競合・途中失敗の
  tests があり、共通 lifecycle は callback / 例外設定か lock 寿命変更を要する。
  lock / reload だけの helper は caller の順序知識を減らさない。現状維持、
  共通 lifecycle、小さな helper の案を比較し、現状維持を選んだ。

- **P1 — docs selftest の既存 cache を利用。** 2026-09-07 の同じ作業 tree、
  既存 cache を使った macOS 単独実行では docs selftest は 20.08 秒、
  fast mutation は 10.97 秒。docs selftest に限定した `-trimpath` 実験は
  12.15 秒で全対照が成功した。fixture の絶対パスを build 入力から外し、
  内容が異なる extractor は引き続き別に build する。共有 binary や新しい
  cache は作らない。mutation は baseline の検証を保ち、並列数を増やさない。
- **C1 — 新しい Go 検証器の対照は既存 CI を利用。** `releaseverify` と固定
  store assertion の Go tests は既存 `go test ./...` の対象となる。CI の新しい
  runtime / job は追加しない。実配布物取得や macOS 上流の実行は release 手順に
  留める。既存 Python 命名 selftest は単独実測 0.61 秒で、今回の遅い工程では
  なかったため移植しない。新しい検証器と store assertion は Go で実装する。

## 計測と並行運用

2026-09-07、実装 candidate `fe5e3c1` の commit gate、正確性レビュー、独立品質
レビューが通過した。全体実行中の docs selftest は 10.95 秒。全体は 25.38 秒で、
version 変更後の未 cache の command tests が 23.432 秒を占めたため、以前の
cache 済み全体時間との短縮比較には使わない。release-evidence の全 mutation、
audit、GoReleaser check / snapshot、naming-agreement、保存した release-smoke、
公開済み v0.18.3 に対する新 Go verifier も成功した。
snapshot は commit 前の同じ実装 tree で実行したため、名称は当時の HEAD を使う
`0.18.3-SNAPSHOT-4a95eb6` だった。`fe5e3c1` 名の snapshot を作った記録ではない。
初回 gate の整形指摘を修正して full gate を再実行し、その後の説明・証拠更新は
docs-check と影響箇所の二段階レビューに限定した。v0.18.4 を公開し、新配布物の
検証も成功した。結果と適用範囲は [ACCEPTANCE.md](../ACCEPTANCE.md) に記録する。

必要性の判定前に全工程の cold/warm 計測を繰り返さない。最初の必須 gate の
工程別ログを初期値として再利用し、残す工程について単独測定する。
cache 有無、同時実行、環境、revision を結果に添え、競合中の値を単独時間と呼ばない。
コマンド時間に加え、full gate の反復回数、レビュー待ち、証拠の再取得も評価する。
恒久的な性能保証や、測定前の削減率・完了時刻は約束しない。

実装は最大二系列。独立した調査・レビューは空き枠で進める。shared docs、gate、
統合は親が持つ。smoke runner と checkout 編集は同時に行わない。
規約変更と実装変更は別コミット。新しい規約が確定するまでは既存の
[AGENTS.md](../../AGENTS.md) と [VALIDATION.md](../VALIDATION.md) の gate を守る。

## 範囲と残す条件

変更対象はこの PJ の検査・実行手順・必要な実装。生成済みグローバル設定、
chezmoi の規約、shared skill の変更は含めない。PJ に委ねられた検証手段と
文書更新対象は見直すが、正確性・独立品質レビュー自体は維持する。

認証情報を保護する隔離、拒否、復元、競合の検出能力を保つ。実機認証が必要な
検証は対象 session の利用状態を確認して直列実行する。実機停止待ちに、独立した
調査・隔離検証を進められる構成にする。

認証の帰属、refresh 所有者、移動 directory、symlink migration、Codex rotation の
未測定動作はこの milestone の実装に混ぜない。Windows、TUI、Tier 拡張も対象外。
リリース版は採用差分の確定後に決め、採用しなかった改善も根拠付きの判断で閉じる。
