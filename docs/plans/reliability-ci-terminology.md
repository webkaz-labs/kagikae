# 認証の保全・CI・用語統一

## 到達点

次期リリースに複数テーマをまとめる。現行機能の信頼性・保守性を改善し、
独立した調査と編集を並行する。認証の保全・修復に必要なリファクタは認証テーマに
含める。Windows、TUI、対応ツールやコマンドの拡張は対象外。

## 作業と採用条件

| テーマ | 作業 | 完了条件 |
|---|---|---|
| 認証の保全・修復 | 合成 credential で誤帰属・修復失敗を調査し、既存 backup consumer の復元先・保持条件を比較 | 再現、consumer 棚卸し、候補比較、独立レビューを一巡し、安全な変更を実装するか、欠けた前提を具体化して保留する |
| CI build | 既存 job 内の build の検出効果と追加時間を確認 | 独立した失敗対照と費用を根拠に採否を決め、採用時は既存 Go 環境で build を実行する |
| 用語統一 | glossary の bound directory に説明・コメントを合わせる | 現在の表記を分類して統一し、歴史的引用などの除外理由を確認する。識別子・コマンド・JSON token は変更しない |

認証の実装は、credential の帰属、保存コピーの復元先、安全な保持・削除条件が
決まってから行う。未知の upstream 挙動や広範な backup model の再設計が必要なら、
不足している証拠と次の判断を残して持ち越す。新しい拒否や修復操作など製品判断が
必要な場合は、具体的な選択肢を提示してユーザーに確認する。

## 進め方と検証

認証調査と CI 測定を並行し、用語は棚卸し後に競合しないファイルから編集する。
共有文書は統合担当が更新し、同じファイルの実装は直列化する。独立して完成した
テーマを認証調査の未決着だけで無期限に待たせない。

論理単位ごとのコミット前検証と正確性・独立品質レビューは
[AGENTS.md](../../AGENTS.md) に従う。指摘対応後は影響範囲を再レビューする。
統合後にリリース検証をまとめて実施し、認証動作を変更した場合は影響する
実機受入を行う。変更しない場合の既存結果の再利用は
[ACCEPTANCE.md](../ACCEPTANCE.md) の適用条件で判断する。
新しい汎用検査器や用語 linter は追加しない。

## 状態

CI build と用語統一の実装・検証は完了。公開前にユーザーが認証保全の方針決定を
希望したため、認証テーマを実装した。新しい command・config 契約を含む v0.19.0 として検証する。公開手順は [RELEASE.md](../RELEASE.md) に従う。

## 調査結果と採否

当初の調査では認証の挙動変更を保留と判断した。既存の
`TestReloginSaysWhatTheLoginFlowIsAboutToReplace`、
`TestReloginDeclinesALoginItWatchedWhenASiblingHasDrifted`、
`TestRestoreSpecFollowsAMovedStore`、`TestPlansFromBackupMetaCapturesWhatTheRollbackWrites`
をキャッシュなしで実行し、既存の拒否・復元先の挙動を確認した。
`TestR4PruneInterleaved` と `TestR4PruneFewerThanKeepCountable` も通過した。
後者は preserved copy が増えても countable undo target が上限未満なら削除しない
対照であり、独立した relogin 保全の保持上限を既存 prune に任せる根拠にはならない。
新しい誤帰属修正ができたことを示す結果ではない。

consumer の確認では、`internal/cmd/ops.go` が現在の global 復元先、identity の消去、
`ActiveBefore`、復元前 backup を扱い、`backup.go` と `restorecred.go` が undo 対象や
警告を扱う。既存 backup に種別フラグだけを追加する案は採用しない。
帰属の証拠、bound credential の復元先、安全に捨てられるコピーがない場合の扱い、
移動と削除を区別する reader の扱いを決めてから、保全を所有する module を設計する。
未解決事項の正本は [ROADMAP.md](../ROADMAP.md) の各認証項目に残す。

CI build は採用する。Go 1.27.1 の合成 main package から main 関数を省いた対照で、
`go test ./...` が成功し `go build ./...` が失敗した。2026-09-07 の手元の測定では、
この repository の test 後に既存 cache を利用した
`go build -o <temporary-directory>/ ./...` は約 0.38 秒で成功した。
これは Ubuntu runner の所要時間ではない。既存 job の Test 後に同じ build を追加し、
出力先は runner の一時領域に置く。新しい検査器や CI job は作らない。

用語統一は現在の説明・コメントを対象に完了した。残る旧表記は glossary の説明、
合成 fixture、unpinned と直前の pin 操作の記述として分類した。
full gate、正確性レビュー、独立品質レビュー、release-evidence、隔離 release smoke、
上流挙動・脆弱性の audit、GoReleaser 設定検査、naming agreement が通過した。
公開と配布物検証は未完了。実機受入の適用結果は [ACCEPTANCE.md](../ACCEPTANCE.md) に記録する。

## 認証保全の確定方針

ユーザーが採用した復元先は採取元の credential store のみ。現在の割当が採取時と
異なる場合、保存先や割当を確認できない場合は復元を拒否する。global rollback へ
転用せず、帰属不明の bytes を特定 account の snapshot として扱わない。

保全領域が満杯で安全に削除できるコピーがない場合、既存コピーを残して
保全が必要な操作を開始前に停止する。容量確保のための先行削除は行わない。
保存する credential payload の合計は初期値 10 MiB、設定で変更可能とする。
backend overhead を含む物理使用量の上限ではない。保全レコードの一覧と、ID 指定・
明示確認付き削除を用意する。唯一のコピーである可能性を削除確認時に示す。
復元先の現在のコピーも保全してから復元し、その容量が足りなければ復元を開始しない。

同じ採取元・割当・内容は重複保存しない。異なる内容は同じ採取元・割当ごとに
最新3世代を残し、新しいコピーの保存成功後に古い世代を自動削除する。
これは有効性の判定ではなく、ユーザーが承認した履歴上限による整理である。
新しいコピーを保存できる空きがない場合は、古いコピーを消して空けず操作を停止する。
復元で使用中のレコードは処理中に削除しない。復元前の保全によってそのレコードが
保持上限の削除対象になる場合は、変更前に復元を拒否し、明示整理を案内する。

`kae preservation list`、`restore <id>`、`rm <id>` を提供する。
復元先は記録から現在の adapter を通して再解決し、現在の binding・driver・artifact
address が一致する場合に限定する。過去の変更履歴まで判定する世代管理は追加しない。
保存する bytes の所有者は推定せず、account snapshot・global active state・identity
cache を復元対象にしない。現行の bound relogin 対象である Claude と Codex を扱う。
保存先の解決や既存コピーの読取・保全に失敗した場合、login を開始しない。

保全 module が記録、重複判定、容量、保持、削除を所有し、既存 secret backend と
artifact IO を再利用する。quota と記録操作はロックで直列化し、対話 login の間は
quota lock を保持しない。kae のロックは upstream 自身の refresh を止めない。
合成テストで失敗順序・中断・容量・重複・復元先変化を確認し、実機受入前には
使用中のセッションが停止していることを改めて確認する。

新しい command と設定を追加するため公開対象は v0.19.0 とする。
CI・用語統一の検証結果はその差分の証拠として保持するが、認証実装後に必要な
検証と実機受入を行う。以前の maintainer-only 適用判断はこのリリースには使用しない。
