# 配布物検証の終了と回収

## 到達点

次回 patch リリースの対象は maintainer 用 `scripts/releaseverify` のプロセス所有。
検証の終了後に同じ process group の子孫が動き続け、一時領域への書込や領域の
残留が起きる問題を修正する。認証動作や `internal/runner` には広げない。

## 比較と採否

- **採用：検証コマンドの終了処理。** 2026-09-07 の合成実験では、50 ms の
  context と 50 ms の WaitDelay を持つ shell が timeout で約104 ms、正常終了
  で約63 ms 後に戻り、いずれも child が300 ms後に markerを書いた。待機時間を
  制限するだけでは子孫の停止にならない。停止責任を既存 command seam に集める。
- **保留：behaviour-site hash。** 確認できた Claude artifact は現行版のみで、
  旧新版比較を再実行できる入力が不足。一般的な hash framework は追加しない。
- **据置：CI build の追加。** local と release は既に build する。早期検出の
  価値と時間を別途判断する候補であり、今回の修正は既存 Go test の CI 経路を使う。

独立した設計相談でも、process group の停止だけでは shell の EXIT trap を
失った際の一時領域回収が残るとの指摘を受けた。installer smoke の一時領域も
検証器が所有する親の下に置き、終了順序と回収を一緒に検証する。

## 実装範囲と完了条件

| 作業 | 状態 | 完了条件 |
|---|---|---|
| 再現・比較 | 完了 | timeout と shell 先行終了で遅延書込を観測。実認証は使用しない |
| command の所有と中断 | 未着手 | darwin/linux の owned process group を終了時に停止。通常出力・エラーと時間制限を保持。割込時も回収へ進む |
| installer 一時領域 | 未着手 | canonical smoke runner を再利用し、所有する親の外を削除しない |
| 検証・レビュー | 未着手 | 正常・失敗・timeout・shell 先行終了・割込、所有外 sentinel、既存の証明先行順序。full gate と二段階レビュー |

意図的に別 process group/session へ離脱した子孫や、検証器自体への SIGKILL の
回収は保証しない。外部プロセスを探索して無差別に停止しない。新しい dependency、
汎用プロセス管理 framework、アカウント切替、Windows/TUI/Tier 拡張は対象外。
検査の追加はこの終了契約を確かめるものに限り、変更のない実機認証を繰り返さない。
