# TermTuck

[简体中文](README.md) · [English](README.en.md) · 日本語

TermTuck は、AWS Bedrock と OpenAI 互換エンドポイントに対応するターミナル向けのチャットツールです。Markdown とコードの構文ハイライト付きで応答をストリーミング表示し、画像も添付できます。ローカルに保存する履歴とモデルに送る文脈は別々に管理します。入力の推定トークン数が設定した上限を超える場合は、古い会話ターンから丸ごと省きます。過去の画像を自動で再送することはありません。

## インストール

主な対象は **Windows Terminal + WSL2** です。Windows PowerShell で `wsl -l -v` を実行し、使うディストリビューションの `VERSION` が `2` であることを確認します。未導入の場合は [Microsoft の WSL インストールガイド](https://learn.microsoft.com/ja-jp/windows/wsl/install)を参照してください。Windows Terminal で WSL のプロファイルを開き、以下のコマンドは **WSL 内**で実行します。通常の Linux 環境やリモートの Linux 端末では、画像をファイルパスで添付してください。Windows のスクリーンショットを取り込むには、ローカルの Windows クリップボードと WSL の相互運用機能が必要です。

Python 3.11 以降、Git、pipx または uv を用意します。リポジトリのページで **Code → HTTPS** から URL をコピーし、`git clone` で取得します。クローンしたディレクトリで実行してください。

```bash
pipx install .
pipx ensurepath   # termtuck が PATH にない場合は、実行後に端末を開き直す。
# または: uv tool install .
# 必要に応じて: uv tool update-shell

termtuck --help
```

PyPI では配布していません。クローンしたリポジトリ、またはそこからビルドした wheel を使ってインストールしてください。従来の `bedrock-chat` コマンドも引き続き使えます。

## AWS Bedrock への接続

TermTuck を実行する環境に [AWS CLI v2](https://docs.aws.amazon.com/ja_jp/cli/latest/userguide/getting-started-install.html) をインストールします。Windows 側の AWS CLI のログイン状態は、自動では WSL に引き継がれません。組織で IAM Identity Center を使っている場合の例です。

```bash
aws configure sso --profile work-sso
aws sso login --profile work-sso
aws sts get-caller-identity --profile work-sso

termtuck --profile work-sso --region us-east-1 --model amazon.nova-lite-v1:0 --lang ja
```

プロファイル名、リージョン、モデルは利用環境に合わせて置き換えてください。使うモデルは Bedrock の `ConverseStream` に対応している必要があります。実際に呼び出すリソースに対する IAM 権限 `bedrock:InvokeModelWithResponseStream` も必要です。モデルによっては、基盤モデルの ID ではなく推論プロファイルの ID または ARN を指定します。SSO の期限が切れたら、送信前に `aws sso login --profile work-sso` を実行し直してください。設定済みの AWS プロファイルを使う場合は `--profile` で指定します。プロファイルを指定しない場合は、`AWS_PROFILE` を含む boto3 のデフォルト認証情報チェーンが使われます。

設定を保存するには、既存のファイルを上書きしないように [config.example.toml](config.example.toml) を `$XDG_CONFIG_HOME/bedrock-chat/config.toml`（既定は `~/.config/bedrock-chat/config.toml`）にコピーするか、`--config PATH` でファイルを指定します。`[aws]` に `profile`、`region`、`model_id` を必要に応じて設定してください。AWS の機密情報はこのファイルに書かないでください。

## ローカルの Ollama・互換エンドポイントを使う

Ollama を起動し、`ollama list` で `qwen2.5:1.5b` があるか確認します。なければ Ollama を動かしている環境で `ollama pull qwen2.5:1.5b` を実行してください。その後、TermTuck を使うターミナルで実行します。

```bash
termtuck --provider openai --model qwen2.5:1.5b
```

既定の送信先は `http://localhost:11434/v1/chat/completions` です。TermTuck を介さず接続先とモデルを確認するには、同じターミナルで以下を実行します。推論リクエストを 1 回送信します。

```bash
curl --noproxy localhost -fsS --max-time 60 \
  http://localhost:11434/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen2.5:1.5b","messages":[{"role":"user","content":"Who are you?"}],"stream":false}'
```

接続が拒否される場合は、Ollama が起動しているか、WSL など TermTuck を実行する環境から `localhost` で接続できるかを確認してください。モデルが見つからない場合は、`ollama list` で名前とタグを確認します。TermTuck 内の `/config` で、使用中のバックエンドと接続先を確認できます。

接続先を変更するには `--endpoint <完全なURL>` を指定するか、[設定例](config.example.toml)の `[chat]` で `provider = "openai"` を指定し、`[openai]` を設定します。`curl` では応答するもののストリーミングが未対応、または正常に終了しない場合は、`[openai]` の `stream = false` を指定して TermTuck を起動し直してください。

現在は API キーが不要なサービスに対応しています。HTTP はループバックアドレスに限り、その他の送信先には HTTPS が必要です。`qwen2.5:1.5b` はテキスト専用です。画像送信には画像対応モデルを選んでください。会話履歴と前回のモデルは、バックエンド・互換エンドポイントごとに分けて保存します。既定のバックエンドは Bedrock です。

## UI の表示言語

起動時は `termtuck --lang zh`（簡体字中国語）、`termtuck --lang en`（英語）、`termtuck --lang ja`（日本語）のいずれかを指定できます。

`/lang` は**チャット内のコマンド**です。シェルで `termtuck /lang` と実行するものではありません。チャット内で `/lang` と入力すると、現在の言語と選択肢が表示されます。`/lang zh`、`/lang en`、`/lang ja` を入力すると表示がすぐに切り替わり、選択がローカルに保存されます。`--lang` を付けずに TermTuck を起動し直すと、保存した言語が使われます。

起動時の優先順は `--lang` → チャット内で最後に選択して保存した言語 → 設定ファイルの `[ui]` にある `language` → 簡体字中国語です。設定例：

```toml
[ui]
language = "zh"
```

`--no-history` を指定すると、最後に選択した表示言語の読み書きは行いません。起動オプションや設定ファイルでの指定は引き続き有効で、チャット内でもその起動中に限って切り替えられます。この設定は UI のみに適用され、モデルに送るプロンプトやモデルの応答言語は変更しません。

## チャットと画像添付

- **Enter** で送信、**Esc** を押してから **Enter** で改行します。応答の中断は **Ctrl+C**、コマンド一覧は `/help`、終了は `/exit` です。
- チャット内で `/image "./sample shot.png"` と入力すると、PNG または JPEG を添付できます。`/image list` で送信前の画像を確認し、`/image drop 1` で先頭の画像を、`/image drop all` ですべての画像を取り除けます。送信には画像入力に対応したモデルを選んでください。
- Windows で **Win+Shift+S** を押し、機密情報を含まないスクリーンショットを撮ります。WSL2 のチャット入力欄で **Ctrl+G** を押すか、`/image paste` を入力します。この操作では WSL の相互運用機能を使って Windows PowerShell から画像を読み取ります。端末の通常の貼り付けはテキスト用です。画像を読み込めないときは、WSL 内で `command -v powershell.exe` を確認するか、ファイルとして添付してください。

添付画像は入力欄の中に `[画像 #1]`、`[画像 #2]` のように表示され、その前後の文章を編集できます。マーカーの直後で **Backspace**、直前で **Delete** を押すと、添付画像ごと取り除けます。残った画像だけが送信され、マーカー自体は文章として送信・履歴保存されません。

添付画像はメッセージを送信するときにだけ、入力したメッセージや送信対象の文脈テキストとともに、選択中のバックエンドにだけ送られます。ローカルの履歴は暗号化されません。既定では画像データ自体は履歴に保存しません。`/history` で保存済みの会話を一覧表示し、`/resume <ID>` で再開できます。

## 検証の範囲

自動テストでは合成画像、PowerShell と Bedrock のモック、端末キー入力のシミュレーションを使います。オプションのローカル AWS CLI v2 テストでも、AWS サービスに接続せずにプロファイルと認証情報の処理を確認します。これらのテストだけでは、実際の Windows からの画像取り込みや AWS での認証と推論は確認できません。

実機で確認するには、機密情報を含まないスクリーンショットを Windows のクリップボードに入れて、WSL 内で `termtuck --check-clipboard` を実行します。Bedrock に接続せずに画像の寸法とサイズが表示されることを確認してください。次にチャットでその画像を添付し、画像入力に対応した使用許可のあるモデルに短いテストメッセージと一緒に送信します。応答がストリーミング表示され、トークン使用量が表示されることを確認します。表示言語の保存を確認するには、`/lang en` で切り替えて終了し、`--lang` なしで起動し直します。`/lang` で `en` が表示されることを確認してください。次に `--no-history` で起動し、チャット内で `ja` に切り替えて終了します。再び `--lang` なしで通常起動すると、`/lang` には引き続き `en` が表示されます。
