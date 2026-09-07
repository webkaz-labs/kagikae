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

計画を承認済み。調査を終え、CI build と用語統一を v0.18.6 向けに実装済み。
新しい command・config・JSON 契約を追加しないため patch release とする。公開手順は [RELEASE.md](../RELEASE.md) に従う。

## 調査結果と採否

2026-09-07、認証の挙動変更は今回保留する。既存の
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
