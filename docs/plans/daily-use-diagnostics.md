# 日常切替・診断一覧の計画

## 到達点と状態

計画の製品方針は合意済み、実装は未着手。手動で選んだ global isolated の
mode と account を自動 hook が保持し、設定や一覧 metadata に問題があっても
復旧判断に必要な情報を読めることを目指す。README の利用例もこの契約に合わせる。
実装開始・リリース番号の決定・公開は、この計画の記録とは別の段階とする。

## 対象と順序

| ID | 対象・状態 | 到達点 | 順序 |
|---|---|---|---|
| A | 自動 hook と手動切替（未着手） | 自動実行の意図を明示し、tool ごとの global isolated mode/account を保持する。手動の shared 指定では確実に解除する | 最優先。B 系列と調査・実装を分担可能 |
| B1 | backup / preservation の診断一覧（未着手） | 正常 metadata と分類した問題を併記し、一覧の不完全性を示して非ゼロで終了する | B2 と一覧契約を一緒に決める |
| B2 | 不正 config からの一覧独立（未着手） | config の問題を警告しつつ metadata を列挙する。一覧が完全なら成功可能とする | B1 と同じ一覧経路で扱う |
| C | 利用案内（未着手） | hook 移行・前提、並行利用の mode、dry-run の範囲を入口で説明する | A/B の契約に依存する説明は契約確定後 |

A と B 系列は独立して進められるが、`App` の設定読込みや共通報告経路を
同時編集しない。共有箇所への変更が必要なら担当と順序を先に調整する。

## A: 自動実行は手動の isolated 選択を保持する

自動 hook と通常の `use` の意味を分け、既存 hook は移行する。
既存 `use` への自動実行フラグを第一案とする（仮称 `--auto`）。名称と引数の
組合せは実装前に確定する。`--quiet` は出力抑制だけに使い、動作を分けない。
新しい command や手動選択の永続記録は第一案に含めず、既存の `state.synced`
を使う。通常の手動 `use` の既定 shared と profile 選択の意味を保つ。

| 利用場面 | 期待する動作 |
|---|---|
| default が main、手動で `kae use -i claude side`、その後 cd | hook は Claude の side isolated を保持する。main の credential をその store に再投入しない |
| profile が Claude main と Codex main、Claude だけ side isolated | Claude を保持し、Codex だけ既存の shared 適用対象にする |
| profile の対象がすべて isolated | 保持のための credential 書込みや backup を行わない |
| profile の対象外に isolated tool がある | その tool を変更しない |
| isolated 選択がない | 従来の profile による shared 適用と active 一致時の no-op を保つ |
| 手動で `kae use -s -P main` | active が main と一致していても、対象 tool の global isolation を解除する。他 tool の選択は保持する |
| 手動で `kae use -P main` または `kae use main` | 通常の shared 切替として扱う |
| isolated の参照先欠損・state/fragment 不整合 | hook は別 account に fallback せず、credential を再構築しない。問題と明示操作への案内を出す |
| bound directory へ移動して戻る | directory の binding と global 選択の関係を fixture で確認する。hook が binding や保持対象を上書きしない |

pin の優先関係は推測で説明せず、fragment の環境値、hook の対象選択、directory を
出た後の状態を受入で確認する。非 isolated tool への既存の global shared 適用まで
停止する変更は含めない。account の削除・rename が選択中の global isolation を
拒否する既存契約も維持する。

生成する hook を新しい自動実行形式へ変更する。既存の kae 所有 marker block は
`kae mise init` の再生成で移行し、手書き hook は該当行の更新を案内する。
任意の project や shell 設定を探索して一括書換えする機能は追加しない。

## B: 復旧の入口を metadata の診断一覧として使えるようにする

対象は既存の `kae backup list` と `kae preservation list`。正常な行の通常 metadata
を保ち、読めない・壊れた記録や列挙失敗を分類して併記する。正常行だけの一覧を
完全な一覧として報告しない。一覧が不完全なら人間向け出力と JSON の両方で明示し、
非ゼロで終了する。問題記録の識別子は安全に扱い、payload や生の parse error を
診断欄へ流さない。

config の妥当性と metadata 列挙の成否を分ける。不正 config は現在の
`ExitInvalidConfig`（`2`）で一覧の前に止まるが、B2 では config の問題を警告として
報告し、列挙自体が完全なら成功できるようにする。パス解決の根拠を確認し、読めない
設定から別の保存場所を推測しない。backend を開かず、credential payload を読まない
一覧経路を維持する。

診断一覧の寛容さを mutation に広げない。rollback、restore、rm、retention の既存
検証・拒否を維持する。壊れた backup を読み飛ばして古い backup を自動選択する
rollback は作らない。preservation の有効な pending/deleting metadata を持つ記録を
明示 ID で削除できる既存契約も、一覧用の変更に巻き込まない。壊れた inventory が
削除を拒否する扱いは維持する。

## C: README と契約文書

README は既存の導入・復旧説明を作り直さず、次の利用判断を補う。

- hook の profile 前提と新形式への移行。fresh state の `profile set` だけでは
  default が設定されないため、default 設定または hook の明示 profile を示す。
- 並行セッションには `run -i` または `pin` を選ぶ説明。既定 `run -s` は子の実行中に
  同 tool の共有切替を排他する。同 account の worktree 同時利用の説明は、Claude の
  per-account credential store に範囲を合わせる。
- README の「exactly what would be patched」という dry-run の説明を対象 command に
  合わせる。rollback の dry-run は backend・現在の store・superseded の検査より前に
  backup ID と tool ごとの artifact 数を返すため、詳細な patch や復旧可能性の確認まで
  済んだとは案内しない。
- 問題付き一覧の読み方、不正 config の警告、完全性と終了コード、診断情報の匿名化。

実装時は `docs/CLI.md` と `docs/PRODUCT.md`、completion の契約・生成物、mise の
生成 hook と説明を同時に更新する。状態と JSON の説明は影響する文書へ反映し、
README に詳細契約を重複させない。

## 計画の根拠と再現方法

2026-09-07 の親セッションの temp fixture 検証では、`applyTestApp` で shared の side を記録し、
`runUseIsolated` で main を選んだ後に shared の `runUseBare` で side を指定すると、
`changed=false`、active は side、synced は main、global fragment は残存した。
解除を期待する一時 probe は失敗した。これは fixture の `App` から内部処理を呼ぶ
検証であり、built binary や引数 parser の E2E ではない。CLI の引数との対応は
`CmdUse` の dispatch を読んで確認した。この観測は mode/account の保持方針とは
区別し、A の手動 shared 解除経路の回帰対照に使う。

実際の不正 TOML から `ConfigErr` を持つ fixture `App` と正常 metadata を用意する
一時 probe、および正常 backup と壊れた backup
metadata を同じ temp root に置く一時 probe は、2026-09-07 の親セッションで一覧阻害を
確認する期待値に成功した。再現時も `testApp` 相当の HOME/XDG と file backend の
fixture と backend nil の一覧対照を使い、実保存領域を使わない。一時 probe を
恒久テストがあるという証拠には
扱わず、B の実装では正常行の保持・問題の分類・一覧完全性を受入テストにする。

2026-09-07 の親セッションでは既存の metadata 一覧と profile 解決の対象テストの
成功を確認した。再確認の入口は `TestPreservationListDoesNotClaimIdentityOrExposePayload`
と `TestBareUseProfileResolutionOrder`。backup 側の追加の確認入口は
`TestSaveListLatestRoundTrip` と `TestListEmptyDir` とする。既存の成功を新しい契約の
検証完了とは扱わない。

## 実装前の技術ゲートと受入

未確定なのは合意済みの利用方針を安全に実装するための契約であり、ユーザー方針を
再選択する項目ではない。自動実行フラグの引数排他、mixed profile の対象絞込み後の
lock と部分失敗、問題記録の安全な識別子、JSON field/token と終了コードの優先順位を
実装前に設計・レビューする。正常行の既存契約を読み、追加 field の互換性を確認する。

| 対象 | 受入条件 |
|---|---|
| A の保持・明示解除 | 上の利用場面を synthetic fixture で確認。quiet と非 quiet の意味は同じ。dry-run は書かず、対象外 tool を保持する |
| A の保持結果の表示 | 全対象を保持する場合と mixed profile の場合に、text/JSON で保持・未変更と適用の結果を区別できる。要求 profile が main でも side を保持したなら「main already active」や profile 全体を適用したという誤った報告をしない。upstream の実 identity を確認したとは主張しない |
| A の hook 移行 | 新規生成と所有 block の更新、手書き hook の案内を確認。pin の環境値と global 状態を分けて観測する |
| B1 | 正常のみ、空、正常と破損の混在、読取失敗、列挙不能を対照にし、正常 metadata・分類・完全性・非ゼロを確認する |
| B2 | 不正 config と完全一覧では警告付き成功。不正 config と不完全一覧では不完全性による非ゼロ。backend/payload のアクセスがないことを確認する |
| mutation の維持 | 問題付き一覧を導入しても rollback/restore/rm の対象検証と拒否、既存の明示操作を維持し、自動の古い rollback を行わない |
| C | fresh fixture の profile 設定から hook 利用まで追える。並行利用・dry-run の説明を command 契約と照合する |

検証は [AGENTS.md](../../AGENTS.md) § Validation に従う。実装は full gate、変更する
実行例は該当 consumer の検証を行う。正確性レビュー後に独立品質レビューを行い、
品質修正があれば影響範囲の正確性へ戻る。ドキュメントと永続メモリは対象ごとに
変更要否を判定する。公開の段階では [RELEASE.md](../RELEASE.md) と
[ACCEPTANCE.md](../ACCEPTANCE.md) に従って影響範囲を評価する。

## 今回含めないもの

architecture の対応は既存 module 内での対象選択・診断経路の整理に限る。
共通の一覧 framework、新しい profile model、support bundle、自動修復、retention や
quota の再設計、TUI、tool/platform 拡張は含めない。
[ROADMAP.md](../ROADMAP.md) の認証 research は、それぞれの測定・機構の前提を
維持する。この計画は attribution、refresh、移動した bound directory、symlink の
移行に関する implementation gate を開かない。
