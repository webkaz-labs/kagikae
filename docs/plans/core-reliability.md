# 現行機能の信頼性と検証自動化

## 目的と到達点

現行の切り替え機能を完成に近づけるため、bound-directory index の深い
module 化、認証ストアの命名を確かめる自動化、既知の CLI 不具合修正を
一つの有限な milestone として進める。新しい機能の数ではなく、既存フローの
信頼性と、変更を検証して届けるまでの手間を改善する。

前回からの **Bound-directory index deepening** を第一の柱とする。
directory、fragment、credential store の観測をまとめ、呼び出し側が同じ
列挙処理を組み立て直さずに必要な問いを扱える seam を作る。ただし、観測の
共有と判断の統一は別である。既存 consumer の refusal と skip の違いを
維持し、読み取りの集約を理由に認証情報の扱いを変えない。

この計画のタスク状態は下表だけで管理する。[ROADMAP.md](../ROADMAP.md) は
この milestone と、今回含めない研究・需要待ち項目への索引とする。
現時点は計画確定の段階であり、実装は開始していない。バージョン番号は、
統合した差分の最終 scope を確認して決める。

## 完了条件

- index の共有された観測から既存 consumer が回答を得られ、成功・拒否・
  不完全な列挙の扱いが characterization と移行後の回帰検証で一致する。
- 同一 command 内の再利用は、読み取り回数と mutation 境界を測定して採否を
  決める。memo を採用する場合は更新後の再読を検証する。採用しない場合も
  測定と理由を残せば、この判断は完了できる。
- Claude の login-free な命名検証を maintainer が一つの command で実行でき、
  一致する対照だけでなく、命名が食い違う対照を失敗として検出する。
- help が既存 parser の引数仕様と一致し、値を取る flag の後でも既存の shell
  completion が正しい位置を補完する。
- 選んだ差分について必須チェック、正確性・品質レビュー、文書更新の判定、
  リリース時に必要な検証が完了する。手順の正本は
  [AGENTS.md](../../AGENTS.md) と [RELEASE.md](../RELEASE.md)。

未解決の研究をすべて解くことは完了条件ではない。ただし、選んだフローの
検証で実際にリリースを阻む問題が再現した場合は、研究項目であることを
理由に合格扱いにしない。再現例と影響を記録し、解決または scope の再合意
までリリースを止める。

## タスクと依存関係

すべて未着手。ファイル欄は所有する作業領域を示す。新しいファイルの最終配置は
各タスクの入口で既存構造を確認して決め、共有文書は統合担当だけが編集する。
各行の検証に加えて、各コミット前に [AGENTS.md](../../AGENTS.md) の必須ゲートを
通す。テスト環境と実機検証の境界は [VALIDATION.md](../VALIDATION.md) と
[ACCEPTANCE.md](../ACCEPTANCE.md) に従う。

| ID・状態 | 作業と入口の条件 | 依存 | 所有するファイル・領域 | done と固有の検証 |
|---|---|---|---|---|
| A1 未着手 | **Index characterization**。既存 walker と consumer の観測・error policy・mutation 境界を表にする。 | なし | `internal/cmd/pinindex_test.go`、`internal/cmd/dircred_test.go`、関連既存テスト | 読める fragment、不完全な index、存在しない directory、共有 store、global isolated home の現行判断を区別して再現できる。拒否する対照と通常成功する対照を両方持つ。 |
| A2 未着手 | **共有する観測の seam**。A1 を根拠に index の観測を内部で扱うdeep module を設計・実装する。新たな attribution policy を持ち込まない。 | A1 | `internal/cmd/pinindex.go` と index の実装・テスト領域 | consumer が必要とする観測と completeness を表せる。入口レビューで責務・interface・観測の寿命を確認し、A1 の各形を同じ interface から検証する。 |
| A3 未着手 | **Consumer migration**。reader、refcount、report、rename の利用箇所を移す。移行順は A1 の依存表で決める。 | A2 | `internal/cmd/dircred.go`、`internal/cmd/doctor.go`、関連 consumer・テスト | 各 consumer の拒否と skip の違いを維持する。移行前後で A1 の結果、diagnostic、credential を保持・削除する境界が一致する。共通化の都合で completeness を失わせない。 |
| A4 未着手 | **Command 内の再利用判断**。重複読取を測定し、mutation 後に何を再取得するか決めてから memo の採否を判断する。 | A3 | index と利用 command の実装・テスト | 採用時は command をまたがず、変更後の観測を再取得する対照が通る。`App` field に持たせない。不採用時は測定結果と理由をこの行に記録する。 |
| B1 未着手 | **Shim の封じ込め**。既存の隔離 harness を使い、Claude の起動が実 credential program に到達しない構成を作る。 | なし | `scripts/` の命名検証用領域。既存 smoke harness を変更する必要があれば統合担当と調整 | HOME/XDG と credential program の境界を実行前に検証する。欠けた shim や隔離条件を与えた対照が upstream 起動前に失敗する。実ログインや実 credential store を使わない。 |
| B2 未着手 | **Claude 命名一致**。PATH shim が届くという測定を確認し、Claude と kae の argv を独立に取得して比較する。 | B1 | B1 と同じ script・fixture 領域 | 対象を Claude に限定し、選んだ config/credential directory のケースで両者が同じ item を指すことを示す。kae の期待値だけを再計算する検査にしない。 |
| B3 未着手 | **失敗対照と maintainer command**。命名不一致・観測不能を区別し、保守作業の実行入口に結ぶ。 | B2 | 命名検証の selftest、`mise.toml` の対象 task | 正常対照が通り、意図的な命名差分を検出し、観測できなかった実行を成功扱いにしない。一つの maintainer command で再実行できる。CI への追加は別判断とする。 |
| C1 未着手 | **Help の引数表記**。`kae add` の既存 parser が受け取る引数と root help を合わせる。 | なし。実装枠が空いた時 | `internal/cmd/cmd.go`、help の既存テスト | account の省略可能性を正しく表示する。parser の受理範囲は変えず、help と既存構文の一致を確認する。 |
| C2 未着手 | **Valued-flag completion**。既存 flag registrar と completion backend を使って、flag の値を positional と誤認しないようにする。 | C1 と独立。実装枠が空いた時 | `internal/cmd/flagspec.go`、`internal/cmd/complete.go`、`internal/cmd/completion.go`、既存 completion テスト | bash/zsh/fish で値を別 word とする形、`=` 形、boolean flag、既存 subcommand の補完位置を確認する。flag 一覧の手コピーや generator 全面書き換えはしない。 |
| I1 未着手 | **統合とリリース判断**。各系列の evidence を受理し、共有文書と最終 scope を整える。 | A4、B3、C1、C2 | この計画、`docs/`、`README.md`、`AGENTS.md` などの共有文書は統合担当が所有 | 必須ゲートと二種のレビューを完了し、追跡 Markdown ごとに変更要否を判定する。必要な実機検証を直列実行し、残存 blocker、最終 scope、バージョン判断を記録する。push・公開は別途承認後。 |

## 並行運用

A と B を並行する。実装中は最大二系列とし、第三の agent 枠は read-only な
調査またはレビューに使う。C は空いた実装枠へ入れる。A 内の consumer 移行を
同じファイルに複数 agent で分配しない。B の fixture と封じ込めが成立する前に
upstream の実行へ進まない。

B1 では `smoke-run.sh` の file driver 強制を、keychain 検証のために単純解除
しない。既存の隔離を保ったまま shim を通す実行経路を選ぶ。対象 upstream
が無い場合や、対象版で shim の到達性を実行前に確認できない場合は起動を
拒否する。絶対パスや直接 API による shim 迂回は、安全な fixture で失敗対照
を作り、実 keychain を使って試さない。B3 は missing tool、観測不能、命名不一致、
平文 credential の残存を成功と区別する。空ログだけで隔離の成功を判断しない。

各担当は差分、終了コード、回帰対照の結果を統合担当へ返す。統合担当が
検証と次段への gate を持つ。共有文書、`mise.toml`、全体チェックの調整は
統合担当に集約し、同時編集を避ける。実機の認証を使う検証は直列にし、
実装や login-free 検査の並列枠とは別に扱う。

## 判断の正本と今回は含めないもの

設計時は [ARCHITECTURE.md](../ARCHITECTURE.md) の package、adapter、locking、
caching の境界と、[CREDENTIAL-RULES.md](../CREDENTIAL-RULES.md) の reader・
attribution・保持の規則を読む。この計画はそれらの契約を置き換えない。
命名検証の観測可能性は
[measuring.md](../../.claude/skills/upstream-auth-drift/references/measuring.md)、
上流の測定結果は [VALIDATION.md](../VALIDATION.md) を正本とする。
CLI の契約は [CLI.md](../CLI.md)、用語は [CONTEXT.md](../CONTEXT.md) に従う。

移動した directory の reader 判定、古い identity cache による attribution、
refresh の所有者、bound-store backup の復元先と retention、読めない payload の
修復 policy は research-only のままとする。A の共通化に混ぜて判断を変えない。
必要な技術判断が入口で決まらないタスクは、その部分の実装に進まず、既存の
再現例と候補の反証結果を返す。

Windows、TUI、Tier 昇格、Codex keyring bind の有効化、新しい公開 command、completion
generator の全面刷新は対象外。behavior-site hash と CI の追加拡大も、この
milestone の必須完了項目に増やさない。scope を増やす場合は、関連する
[ROADMAP.md](../ROADMAP.md) の gate を満たしたうえで合意し直す。
