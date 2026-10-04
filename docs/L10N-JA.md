# 日本語表示の用語・文体・文字

kae の人間向け出力を日本語で書く・直すときに読む文書です。英語が正本で、日本語が英語と食い違うときは英語に合わせて直します。

- この文書が持つもの: 英語の語の日本語表記、文体、使える文字。
- 持たないもの: 日本語表示の振る舞い（言語の選択、翻訳しないもの、1 行 1 言語、終了コードや JSON が言語で変わらないこと）は [CLI.md](CLI.md) § Localization が持つ。英語の語の定義と、語を足す・変えたときに表を揃える規則は [CONTEXT.md](CONTEXT.md) が持つ。

## 状態

次の表の「区分」は訳語の確定度を示します。

| 区分 | 意味 |
|---|---|
| 確定 | 既存の日本語文書（README.ja.md、GUIDE.ja.md、PRODUCT.ja.md）が既に使っている訳。変えると既存文書と食い違う |
| 決定 | operator が決めた訳 |

## 用語表

### 利用者が打つ・読む語

| 英語 | 日本語 | 区分 | 備考 |
|---|---|---|---|
| account | アカウント | 確定 | `claude/main` 形式の名前は訳さない |
| profile | プロファイル | 確定 | |
| tool | ツール | 確定 | `claude` `codex` 等の名前は訳さない。「対応ツール」「上流ツール」 |
| upstream | 上流 | 確定 | 「上流ツール」「上流サービス」 |
| adapter | アダプター | 確定 | |
| driver | ドライバー | 決定 | 既存なし。adapter（アダプター）と区別できる語。利用者向け出力にはほぼ出ない |
| artifact | 認証要素 | 決定 | 「成果物」は誤解を招くので使わない。JSON の artifact kind トークンは訳さない |
| companion | 周辺ツール | 決定 | コマンド名 `kae companion` は訳さない。PRODUCT.ja.md の用語表は英語の `companion` のままで、出力文字列では「周辺ツール」を使う |
| companion knob | 設定項目 | 決定 | `kae companion add` に渡す KEY（`email` や `GH_TOKEN`）。KEY の名前は訳さない |
| place | 場所 | 確定 | 初出では「場所」を定義する形（GUIDE.ja.md）を踏襲する |
| session row | セッション記録の行 | 決定 | GUIDE.ja.md の「セッション記録を置く `projects/` 以下の行」に合わせる |
| session / transcript | セッション / セッション記録 | 確定 | |
| mise | mise | 訳さない | 製品名 |

### 仕組みの語

| 英語 | 日本語 | 区分 | 備考 |
|---|---|---|---|
| snapshot | スナップショット | 確定 | kae 自身の保存コピー。ツール側の login state と混ぜない |
| credential | 認証情報 | 確定 | 「資格情報」「クレデンシャル」は使わない |
| auth / authentication | 認証 | 確定 | |
| credential store | 認証ストア | 確定 | 短縮の「ストア」は文脈が自明なときだけ |
| secret store | シークレットストア | 決定 | kae が保存コピーを置くバックエンド（キーチェーン、libsecret、ファイル）。ツール側の認証ストアと混ぜない |
| payload | 保存データ | 決定 | シークレットストアに置いたスナップショット・バックアップ・ログイン識別子の中身。「ペイロード」は使わない |
| isolation store / working store | 作業ストア | 確定 | |
| bound directory | 固定したディレクトリ | 決定 | 英語の散文は bound directory / bind に統一する（pin はコマンド名 `kae pin` だけ）。「固定ディレクトリ」とは書かない。PRODUCT.ja.md の「ディレクトリ固定」は機能名として残す。日本語では bound と pinned の区別が出ない |
| bind（動詞） | 固定する | 確定 | |
| binding（名詞） | 固定の内容 | 決定 | 「バインディング」は既存ゼロなので使わない |
| unpin / unbind | 固定を解除する | 確定 | GUIDE.ja.md「`kae unpin` は固定を解除します」。コマンド名は訳さない |
| mode | モード | 確定 | |
| `shared` mode | 共有モード / 共有環境 | 確定 | フラグ・設定値 `-s` `shared` は訳さない |
| `isolated` mode | 独立モード / 独立環境 | 確定 | 「隔離」は mode 名に使わない |
| `tree` mode | ツリーモード | 確定 | 「ツリー」単独で mode を指さない |
| fragment | フラグメント | 確定 | パス `.config/mise/conf.d/kagikae.toml` と併記する |
| store link | ストアへのリンク | 確定 | GUIDE.ja.md の「そのツールのストアへの symlink も張ります」に合う。symlink は技術語として訳さない |
| bond dir | 利用者向けでは出さない | 決定 | CONTEXT.md の shared config dir と同一物。見せるならパス（`shared`）を出す |
| pin-id | 訳さない | 確定 | パス断片（`isolation/<pin-id>/`）として英語のまま出る |
| breadcrumb | 固定したディレクトリの記録 | 決定 | 内部語。「パンくず」は誤解を招くので不可。fragment と混ぜない |
| reader | 参照元 | 決定 | credential store を読んでいる設定ディレクトリ。「証人」系の語は避ける |
| harvest | 退避 | 決定 | 失う前に残る場所へコピーする意味（元は残る）。「退避コピー」「退避する」。「回収」は元を取り去る含みがあるので使わない |
| capture back | 取り込み直し | 決定 | `kae relogin` が再ログイン後に行う harvest 1 回。harvest（退避）の同義語ではないので訳語を分ける |
| preservation record | 保全記録 | 確定 | コマンド `kae preservation restore <id>` は訳さない |
| backup | バックアップ | 確定 | 保全記録とは別の復元経路。混ぜない |
| restore / rollback | 復元 / `kae rollback` | 確定 | rollback はコマンド名なので訳さない。「復旧」は障害対応の見出し語（「認証の復旧」）として復元と区別する |
| tombstone | 失効マーカー | 決定 | ツール自身が、自分のログインが失効したと記録するために上書きした認証情報 |
| identity cache | ログイン中アカウントの記録 | 決定 | credential ではなく account の証拠であることが読める。短縮は「アカウント記録」 |
| login identity | ログイン識別子 | 決定 | ツールが報告するログインの識別値（メールアドレスや UUID）。identity cache（ログイン中アカウントの記録）とは別 |
| supersedes / orderable | 訳さない | 決定 | 述語名は利用者向けでない。文では「新しい」「新旧を決められない」と書く |
| dry-run | `--dry-run` | 確定 | 名詞としては「変更予定の確認」。「ドライラン」は使わない |
| lock | ロック | 確定 | 「ロック競合」 |
| picker | ピッカー | 確定 | |
| usage quota | 利用枠 | 確定 | |
| login / relogin | ログイン / 再ログイン | 確定 | `kae relogin` は訳さない |
| expire / revoke（live login） | 失効 | 確定 | 期限切れは「期限切れ」 |
| switch | 切替 / 切り替える | 決定 | 名詞は送り仮名なしの「切替」、動詞は送って「切り替える」に固定する |
| config file | 設定ファイル | 確定 | |
| env var | 環境変数 | 確定 | `KAE_LANG` 等の名前は訳さない |
| child process | 子プロセス | 確定 | |
| refresh token | リフレッシュトークン | 決定 | 動詞の refresh は「リフレッシュ」。スナップショットを取り直す recapture は「更新」 |
| adopt / declined to adopt | 取り込む / 取り込みを見送る | 決定 | |
| pre-split | 分割前 | 決定 | |
| capture time / Captured（見出し） | 登録日時 | 決定 | |
| Identity（見出し） | 識別子 | 決定 | 本文の login identity は「ログイン識別子」 |
| Active / In effect（見出し） | 有効 | 決定 | |
| Notes / Current / Source / Applies / Level / Kind / Root（見出し） | 備考 / 現在 / 取得元 / 適用先 / レベル / 種別 / ルート | 決定 | |
| present / absent | あり / なし | 決定 | 人向けのセル語。JSON は bool |
| re-login now / N day(s) left | 今すぐ再ログイン / 残り N 日 | 決定 | |
| keychain item / keyring | キーチェーン項目 / キーリング | 決定 | |
| codex home | codex ホーム | 決定 | |
| file driver | ファイルドライバー | 決定 | |
| env profile | 環境変数プロファイル | 決定 | |
| secret backend | シークレットストア | 決定 | secret store と同一視する |
| cancel | キャンセル | 確定 | |

### 型と記号

| 項目 | 現在の案 | 区分 | 備考 |
|---|---|---|---|
| did-you-mean | 「もしかして: X」 | 決定 | X は候補をそのまま入れる |
| 確認プロンプト | 「続行する場合は y を入力してください [y/N]」 | 決定 | 受け付ける答えが言語で変わらないことは [CLI.md](CLI.md) § Localization が持つ。それ以外の疑問文は作らない |
| 対処コマンドの囲み | バッククォートで囲まない | 決定 | 端末出力は Markdown ではない |
| 利用枠のセルの区切り `·`（U+00B7） | 変えない | 決定 | 「使える記号」の節を参照 |

### 訳さないもの

訳さない集合は [CLI.md](CLI.md) § Localization の "What is never localized" が持ちます。ここでは日本語の文に埋め込むときの書き方だけを決めます。

- コマンド名・フラグ名・tool 名・account 名・profile 名は、文中の日本語に埋め込んでもそのまま書く（「アカウント `claude/main`」）。

## 文体

### 敬体と体言止め

既存の日本語文書は敬体（です・ます）で統一されているので、人間向け出力も合わせます。

| 場面 | 規約 | 例 |
|---|---|---|
| 説明・警告・note の文 | 敬体 | 「設定は変更しません。」 |
| 表の見出し・ラベル・箇条書き | 体言止め | 「アカウント」「利用枠」 |
| 失敗の述語 | 「〜できません」「〜に失敗しました」 | |
| 対処（次にすること） | 「〜してください」。命令形と依頼形を混ぜない | 「kae add claude main を実行してください」 |
| 文末の句点 | 警告・note・check の message は句点で終える。stdout の報告行（reportf）は英語の句点の有無に合わせる（英語に句点が無ければ付けない） | 「環境変数プロファイル claude/main を削除しました」 |

### 対処の書き方

英語側は対処を `; run: kae X` の形に統一します（operator 決定）。日本語は同じ内容を「…してください」型で書き、コマンドは英語のまま訳しません。

- 理由を先、対処を後に置く。「A は B のため C できません。D を実行してください」。
- 条件は「〜の場合は」で前置し、対処を文末に置く。
- 複数の手順は番号でなく別の行にし、1 行に 1 手順とする。
- 対処コマンドはバッククォートで囲まない（「型と記号」の表）。

### エラー・警告の書き出し

英語の `kae: cannot <do X>: <cause>` に対応させます。接頭辞は英語固定です。

| 英語の型 | 日本語の型 |
|---|---|
| `kae: cannot X: <cause>` | `kae: Xできません: <原因>`（X は体言） |
| `kae: warning: X` | `kae: warning: X。`（敬体、文末は句点） |
| 原因が外部エラー | `kae: Xできません: <英語の原因をそのまま>` |

コロンは ASCII の `: `（半角コロンと半角空白）を使い、全角の「：」は使いません。

### 句読点・括弧・空白

- 句読点は「、」「。」のみ。「，」「．」は使わない。
- 日本語の文中の括弧は全角「（ ）」。括弧内のコロンは ASCII（「（例: …）」）。半角 `( )` は、コマンド出力例のように verbatim の部分と、名前やパスの直後だけに使う。
- 半角英数と全角文字の間には半角空白を入れる。句読点・括弧・バッククォートの隣には入れない。
- 引用は鉤括弧「 」を使う。曲線引用符は使わない。
- 数値は算用数字、助数詞は漢字またはかな（「1 つ」「3 回」）。
- 確認プロンプトと did-you-mean の型は「型と記号」の表に従い、それ以外の疑問文は作らない。

## 使える文字

### 事実

`internal/cmd/text.go` の `displayWidth` は、SGR を除去し、結合文字（`Mn` `Me`）と U+200D を幅 0 とし、残りを `golang.org/x/text/width` の `LookupRune(r).Kind()` で分類します。`EastAsianWide` と `EastAsianFullwidth` だけを 2、それ以外（Ambiguous を含む）をすべて 1 と数えます（x/text の版は go.mod が決める）。表の列はこの幅で揃えられます（[CLI.md](CLI.md) § Output Rules、§ Localization）。

Ambiguous は 1 と数えられますが、East Asian Ambiguous を 2 桁で描く端末では列が右へずれます。日本語の文字列は日本語ロケールで読まれる前提なので、Ambiguous を使いません。

次の分類は x/text の `width.LookupRune` を引いた結果の例です。Ambiguous の判定の正本は [VALIDATION.md](VALIDATION.md) § Output language in tests のカタログテストで、それ以外の不可は文体規約です。

| 範囲または文字 | 分類 | 可否 |
|---|---|---|
| ひらがな、カタカナ（「・」(U+30FB)「ー」(U+30FC) を含む）、CJK 統合漢字 | Wide | 可 |
| 全角形 U+FF01 から U+FF60 | Fullwidth | 幅計算上は可。ただし「，」「．」と全角英数は文体規約で不可 |
| 半角カナ U+FF61 から U+FF9F | Halfwidth（幅 1） | 可だが使わない |
| ASCII | Narrow | 可 |
| 列挙した記号: Latin-1 補助「× ÷ ° ± · § ¼ ½ é ü」、「…」「‥」「—」「―」「–」「※」、曲線引用符、矢印「→ ← ↑ ↓ ⇒ ⇔」、数学記号「≠ ≦ ≧ ∴ ∞ ≈」、罫線「─ │ ┌」、幾何図形「○ ● □ ■ △ ▲ ◆」、囲み数字「① ②」、「℃」「№」「™」「®」 | Ambiguous | 不可 |
| 「、」「。」「「」」「『』」「【】」「〜」(U+301C) など U+3000 から U+303F の記号（U+303F を除く） | Wide（U+3000 は Fullwidth） | 可 |
| 「（）」「！」「？」「％」「＋」「－」「＝」「／」と「～」(U+FF5E) | Fullwidth | 可 |
| 「：」「；」 | Fullwidth | 幅計算上は可。ただしコロン類は ASCII を使う規約で不可 |
| 「✓」「✗」「⚠」「➜」「⋯」 | Neutral（幅 1） | 計算は通るが、端末が絵文字幅（2）で描く場合があるので使わない |

中黒は U+30FB（Wide）を使い、U+00B7（Ambiguous）は使いません。

### 代替表記

| 英文の記号 | 日本語での代替 |
|---|---|
| `—` 区切り・補足 | 文を分けて「。」にする。または「、」「: 」、全角括弧 |
| `…` 省略 | 文中は「など」「ほか」。記号が必要なら ASCII の `...` |
| `→` 対応 | 「a から b」「a を b に」。機械的な矢印は ASCII の `->` |
| `×` 個数 | 「3 回」「3 件」 |
| `※` 注記 | 「注: 」または別の文 |
| `°` | 「度」 |
| `·` 区切り | 「、」または `/`。語の連結には「・」(U+30FB) |
| 曲線引用符 | 「 」。コード風の値は ASCII の `"` |

### 使える記号

- ASCII のすべて
- 日本語: 、 。 「 」 『 』 （ ） 【 】 ・ ー 〜 ～ ！ ？ ％ ＋ － ＝ ／
- ひらがな、カタカナ、漢字、々、〇
- 規約で避ける: 全角英数、半角カナ、Neutral の記号

この規約は kae が出す日本語の文字列に掛かります。英語側の出力やコメントが使う `—` `…` `→` `×` `·` は対象外です。既存の日本語文書にある `·` は、kae の出力例の引用か文書内のナビゲーション行です。

この規約の例外が 1 つあります。利用枠のセルは `internal/usagelimit/limit.go` の `Separator`（`" · "`、U+00B7）で部品を連結します。これは言語に依存しない値で、日本語の表にもそのまま出ます。VALIDATION の機械検査が見るのは日本語カタログの文字列なので、この値は検査の外にあります。operator の決定で変えません。Ambiguous を 2 桁で描く端末では、この区切りの分だけ列が 1 桁ずれますが、許容します。

### 機械検査

日本語文字列に East Asian Ambiguous の文字が含まれないことは、[VALIDATION.md](VALIDATION.md) § Output language in tests のカタログテストが検査します。分類器は `displayWidth` と同じ `width.LookupRune` で、規約と検査が一致します。この文書は検査を持ちません。
