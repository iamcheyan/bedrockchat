"""English and Japanese text for the terminal interface."""

MESSAGES = {
    "[图片 #{number}]": ("[Image #{number}]", "[画像 #{number}]"),
    "本次运行的图片标记已用完，请重启 bedrockchat。": (
        "No more attachment markers are available. Restart bedrockchat.",
        "画像のマーカーを使い切りました。bedrockchat を再起動してください。",
    ),
    "/new [名称]": ("/new [name]", "/new [名前]"),
    "/history [关键词]": ("/history [keyword]", "/history [キーワード]"),
    "/show [轮数]": ("/show [turns]", "/show [ターン数]"),
    "/rename <ID> <名称>": ("/rename <ID> <name>", "/rename <ID> <名前>"),
    "/model [别名或 ID]": ("/model [alias or ID]", "/model [別名または ID]"),
    "/image <路径>": ("/image <path>", "/image <パス>"),
    "/image drop [序号|all]": ("/image drop [number|all]", "/image drop [番号|all]"),
    "/image reuse <轮次> <序号>": ("/image reuse <turn> <number>", "/image reuse <ターン> <番号>"),
    "/export [文件.md]": ("/export [file.md]", "/export [ファイル.md]"),
    "创建会话": ("Create a session", "セッションを作成"),
    "列出会话，可按名称或消息搜索": (
        "List sessions; optionally search names and messages",
        "セッションを一覧表示。名前やメッセージで検索可能",
    ),
    "恢复或切换会话（支持唯一前缀）": (
        "Resume or switch sessions (unique ID prefixes accepted)",
        "セッションを再開・切り替え（一意な ID の接頭辞も可）",
    ),
    "查看当前会话最近的本地记录，默认 5 轮": (
        "Show recent local history (default: 5 turns)",
        "現在のセッションの履歴を表示（既定は直近 5 ターン）",
    ),
    "重命名本地会话": ("Rename a local session", "ローカルセッションの名前を変更"),
    "永久删除指定会话及其已保存图片": (
        "Permanently delete a session and its saved images",
        "指定したセッションと保存済み画像を完全に削除",
    ),
    "查看或切换模型": ("Show or change the model", "モデルを表示・切り替え"),
    "清空发送上下文，保留本地历史和用量": (
        "Clear the model context but keep local history and usage",
        "モデルに送るコンテキストのみをリセット。履歴・使用量は保持",
    ),
    "查看当前会话累计用量及可估算的费用": (
        "Show session usage and estimated cost when available",
        "セッションの累計使用量と概算費用を表示",
    ),
    "附加指定文件；路径有空格时加引号": (
        "Attach a file; quote paths with spaces",
        "ファイルを添付。空白を含むパスは引用符で囲む",
    ),
    "手动获取 Windows 剪贴板图片": (
        "Attach an image from the Windows clipboard",
        "Windows のクリップボードから画像を添付",
    ),
    "移除指定图片，省略序号时移除最后一张": (
        "Remove an attached image (default: the last one)",
        "添付画像を削除（番号を省略すると最後の画像）",
    ),
    "重新附加当前会话中主动保存过的历史图片": (
        "Reattach an image saved in this session's history",
        "このセッションの履歴に保存された画像を再添付",
    ),
    "导出当前会话为 Markdown（不覆盖现有文件）": (
        "Export this session as Markdown (no overwrites)",
        "このセッションを Markdown で書き出す（上書きなし）",
    ),
    "查看当前配置（不显示 AWS 凭证或系统提示词）": (
        "Show settings without AWS credentials or the system prompt",
        "設定を表示（AWS 認証情報とシステムプロンプトを除く）",
    ),
    "查看或切换界面语言": ("Show or change the interface language", "表示言語を確認・切り替え"),
    "显示本帮助": ("Show this help", "このヘルプを表示"),
    "退出": ("Exit", "終了"),
    "支持图片、本地历史和 Token 预算的终端聊天": (
        "Terminal chat with images, local history, and a token budget",
        "画像・ローカル履歴・トークン予算に対応したターミナルチャット",
    ),
    "模型后端（默认 bedrock）": (
        "Model backend (default: bedrock)",
        "モデルのバックエンド（既定：bedrock）",
    ),
    "OpenAI 兼容后端的完整 Chat Completions URL": (
        "Full Chat Completions URL for the OpenAI-compatible backend",
        "OpenAI 互換バックエンドの Chat Completions URL（完全な URL）",
    ),
    "后端：{provider}": ("Backend: {provider}", "バックエンド：{provider}"),
    "端点：{endpoint}；请求流式输出：{stream}": (
        "Endpoint: {endpoint}; request streaming: {stream}",
        "エンドポイント：{endpoint}；ストリーミング要求：{stream}",
    ),
    "后端必须是 bedrock 或 openai。": (
        "The backend must be bedrock or openai.",
        "バックエンドは bedrock または openai を指定してください。",
    ),
    "--endpoint 仅可用于 --provider openai。": (
        "--endpoint can only be used with --provider openai.",
        "--endpoint は --provider openai の場合にのみ使用できます。",
    ),
    "兼容端点必须是完整的 /chat/completions URL；仅回环地址允许 HTTP，不得包含凭证、查询或片段。": (
        "Use a full /chat/completions URL without credentials, a query, or a fragment. HTTP is only allowed on loopback.",
        "認証情報・クエリ・フラグメントのない /chat/completions の完全な URL を指定してください。HTTP はループバックのみ使用できます。",
    ),
    "位置参数": ("positional arguments", "位置引数"),
    "选项": ("options", "オプション"),
    "显示帮助并退出": ("Show help and exit", "ヘルプを表示して終了"),
    "显示版本并退出": ("Show the version and exit", "バージョンを表示して終了"),
    "显式读取指定的 TOML 文件": (
        "Read the specified TOML file",
        "指定した TOML ファイルを読み込む",
    ),
    "界面语言：中文、英语或日语": (
        "Interface language: Chinese, English, or Japanese",
        "表示言語：中国語、英語、日本語",
    ),
    "模型 ID 或别名；Bedrock 也支持推理配置文件 ID/ARN": (
        "Model ID or alias; Bedrock also accepts an inference profile ID/ARN",
        "モデル ID または別名。Bedrock では推論プロファイルの ID/ARN も指定できます",
    ),
    "本地估算的输入 Token 预算": (
        "Locally estimated input token budget",
        "ローカルで見積もる入力トークンの予算",
    ),
    "请求的最大输出 Token": (
        "Maximum output tokens in the request",
        "リクエストの最大出力トークン数",
    ),
    "不读写历史、图片或本地偏好": (
        "Do not read or save history, images, or local preferences",
        "履歴・画像・ローカル設定を読み書きしない",
    ),
    "不保存图片字节到本地历史": (
        "Do not save image data to local history",
        "ローカル履歴に画像データを保存しない",
    ),
    "仅测试并显示剪贴板图片尺寸，不联网": (
        "Check the clipboard image and show its dimensions (offline)",
        "クリップボード画像を確認して寸法を表示（オフライン）",
    ),
    "请输入正整数序号。": ("Enter a positive number.", "正の整数を入力してください。"),
    "未命名": ("Untitled", "無題"),
    "图片 {count} 张": ("Images: {count}", "画像 {count} 枚"),
    "Ctrl+G 粘贴截图 · Esc+Enter 换行 · Enter 发送": (
        "Ctrl+G paste image · Esc+Enter newline · Enter send",
        "Ctrl+G 画像を貼り付け · Esc+Enter 改行 · Enter 送信",
    ),
    "当前模型不支持图片，请用 /model 切换到视觉模型。": (
        "This model does not accept images. Use /model to choose a vision-capable model.",
        "このモデルは画像に対応していません。/model で画像対応モデルに切り替えてください。",
    ),
    "最多附加 {count} 张图片。": (
        "Attach up to {count} images.",
        "画像は最大 {count} 枚まで添付できます。",
    ),
    "图片合计超过 15 MB，请移除部分图片或减少其尺寸。": (
        "The images exceed 15 MB in total. Remove or resize some images.",
        "画像の合計が 15 MB を超えています。画像を削除するか縮小してください。",
    ),
    "已附加 #{count}（{image}）": (
        "Attached #{count} ({image})",
        "#{count} を添付しました（{image}）",
    ),
    "没有这个待发送图片序号。": (
        "No pending image with that number.",
        "指定された番号の添付画像はありません。",
    ),
    "用法：/image <路径> | paste | drop [序号|all] | reuse <轮次> <序号>": (
        "Usage: /image <path> | paste | drop [number|all] | reuse <turn> <number>",
        "使い方：/image <パス> | paste | drop [番号|all] | reuse <ターン> <番号>",
    ),
    "会话累计（已知）Token：输入 {input} / 输出 {output} / 总计 {total}": (
        "Session tokens (known): input {input} / output {output} / total {total}",
        "セッションの累計トークン（判明分）：入力 {input} / 出力 {output} / 合計 {total}",
    ),
    "；{count} 次用量未知": ("; unknown usage: {count}", "；使用量不明のリクエスト：{count} 件"),
    "；已定价部分预估 US${cost:.8f}": (
        "; estimated cost for priced usage: US${cost:.8f}",
        "；単価設定済み分の概算 US${cost:.8f}",
    ),
    "（{count} 次未定价）": (
        " (unpriced requests: {count})",
        "（単価未設定のリクエスト：{count} 件）",
    ),
    "本会话还没有对话。": (
        "This session has no messages yet.",
        "このセッションにはまだ会話がありません。",
    ),
    "（仅图片）": ("(images only)", "（画像のみ）"),
    "  图片 #{index}：{name} · {width}×{height}": (
        "  Image #{index}: {name} · {width}×{height}",
        "  画像 #{index}：{name} · {width}×{height}",
    ),
    "已保存": ("saved", "保存済み"),
    "未保存": ("not saved", "未保存"),
    "由 boto3 解析": ("resolved by boto3", "boto3 が解決"),
    "boto3 默认凭证链": ("boto3 default credential chain", "boto3 の既定の認証情報チェーン"),
    "Region：{region}；Profile：{profile}": (
        "Region: {region}; Profile: {profile}",
        "リージョン：{region}；プロファイル：{profile}",
    ),
    "模型：{model}；输入预算（估算）：{input}；输出上限：{output}": (
        "Model: {model}; input budget (estimated): {input}; output limit: {output}",
        "モデル：{model}；入力予算（推定）：{input}；出力上限：{output}",
    ),
    "历史：{history}；图片保存：{images}；图片长边/字节上限：{edge} px / {size} B": (
        "History: {history}; image saving: {images}; image long edge/byte limits: {edge} px / {size} B",
        "履歴：{history}；画像保存：{images}；画像の長辺・容量の上限：{edge} px / {size} B",
    ),
    "开启": ("on", "有効"),
    "关闭": ("off", "無効"),
    "未设置": ("not set", "未設定"),
    "无法保存下次启动的模型，请检查本地历史目录权限。": (
        "Could not save the model for next time. Check local history directory permissions.",
        "次回用のモデルを保存できませんでした。ローカル履歴ディレクトリの権限を確認してください。",
    ),
    "会话：{id} · {name}；/show 查看记录。": (
        "Session: {id} · {name}. Use /show to view history.",
        "セッション：{id} · {name}。/show で履歴を表示できます。",
    ),
    "没有匹配的本地会话。": (
        "No matching local sessions.",
        "該当するローカルセッションはありません。",
    ),
    "已重命名：{name}": ("Renamed: {name}", "名前を変更しました：{name}"),
    "已删除：{id}": ("Deleted: {id}", "削除しました：{id}"),
    "当前模型：{model}": ("Current model: {model}", "現在のモデル：{model}"),
    "支持图片": ("supports images", "画像対応"),
    "仅文本": ("text only", "テキストのみ"),
    "视觉未标注": ("image support unknown", "画像対応は未確認"),
    "已切换：{model}": ("Switched model: {model}", "モデルを切り替えました：{model}"),
    "已清空发送上下文；原始历史与累计用量保持不变。": (
        "Model context cleared. Local history and total usage are unchanged.",
        "送信コンテキストをリセットしました。ローカル履歴と累計使用量は変更されていません。",
    ),
    "已导出：{path}": ("Exported: {path}", "書き出しました：{path}"),
    "界面语言：{language}": ("Interface language: {language}", "表示言語：{language}"),
    "无法保存界面语言，下次启动时可能不会记住。": (
        "Could not save the interface language for next time.",
        "表示言語を保存できませんでした。次回起動時に反映されない場合があります。",
    ),
    "界面语言已切换为：{language}": (
        "Interface language set to {language}.",
        "表示言語を {language} に切り替えました。",
    ),
    "用法：/lang [zh|en|ja]": ("Usage: /lang [zh|en|ja]", "使い方：/lang [zh|en|ja]"),
    "Enter 发送 · Esc 后按 Enter 换行 · Ctrl+G 粘贴图片 · Ctrl+C 中断 · Ctrl+R 搜索输入历史。": (
        "Enter sends · Esc then Enter inserts a newline · Ctrl+G pastes an image · Ctrl+C interrupts · Ctrl+R searches input history.",
        "Enter 送信 · Esc の後に Enter で改行 · Ctrl+G 画像を貼り付け · Ctrl+C 中断 · Ctrl+R 入力履歴を検索。",
    ),
    "光标在图片标记后按 Backspace，或在标记前按 Delete，可移除该图片。": (
        "Press Backspace after an image marker or Delete before it to remove the image.",
        "画像マーカーの直後で Backspace、直前で Delete を押すと画像を削除できます。",
    ),
    "普通文字以斜杠开头时可用 // 转义；多行输入总被当作消息。": (
        "Use // to send text starting with a slash. Multiline input is always sent as a message.",
        "スラッシュで始まる文は // で入力できます。複数行の入力は常にメッセージとして送信されます。",
    ),
    "未知命令或参数不正确。用 /help 查看。": (
        "Unknown command or invalid arguments. See /help.",
        "コマンドまたは引数が不正です。/help を確認してください。",
    ),
    "命令参数的引号或转义不完整。": (
        "The command contains an unfinished quote or escape.",
        "コマンド内の引用符またはエスケープが閉じられていません。",
    ),
    "请先用 /model <ID> 设置模型，或在启动时提供 --model。": (
        "Choose a model with /model <ID> or launch with --model.",
        "/model <ID> でモデルを指定するか、起動時に --model を指定してください。",
    ),
    "当前模型不支持图片；请切换模型，或先 /image drop all。": (
        "This model does not accept images. Switch models or run /image drop all.",
        "このモデルは画像に対応していません。モデルを切り替えるか /image drop all を実行してください。",
    ),
    "发送中 · 输入估算 {tokens} Token": (
        "Sending · estimated input: {tokens} tokens",
        "送信中 · 入力は推定 {tokens} トークン",
    ),
    " · 略去 {turns} 个最早轮次": (
        " · oldest turns omitted: {turns}",
        " · 古い {turns} ターンを省略",
    ),
    " · Ctrl+C 中断": (" · Ctrl+C to interrupt", " · Ctrl+C で中断"),
    "停止原因：{reason}": ("Stop reason: {reason}", "停止理由：{reason}"),
    "本轮未能写入持久历史：{error}": (
        "Could not save this turn to local history: {error}",
        "このターンをローカル履歴に保存できませんでした：{error}",
    ),
    "本次 Token：未知（服务未返回用量；不代表零费用）。": (
        "Tokens for this request: unknown (the service did not report usage; charges may still apply).",
        "今回のトークン数：不明（サービスが使用量を返しませんでした。課金される場合があります）。",
    ),
    "；预估 US${cost:.8f}": ("; estimated cost: US${cost:.8f}", "；概算 US${cost:.8f}"),
    "本次 Token：输入 {input} / 输出 {output} / 总计 {total}": (
        "Request tokens: input {input} / output {output} / total {total}",
        "今回のトークン数：入力 {input} / 出力 {output} / 合計 {total}",
    ),
    "已取消图片粘贴。": ("Image paste cancelled.", "画像の貼り付けをキャンセルしました。"),
    "已移除最后一张图片。": ("Removed the last attached image.", "最後の添付画像を削除しました。"),
    "你 › ": ("You › ", "あなた › "),
    "剪贴板：{size} · {format}；未联网、未保存。": (
        "Clipboard: {size} · {format}; no network or file writes.",
        "クリップボード：{size} · {format}；通信・保存はしていません。",
    ),
    "请在交互式 WSL2 终端运行；可用 --help 查看选项。": (
        "Run in an interactive WSL2 terminal. Use --help to see options.",
        "対話型の WSL2 ターミナルで実行してください。オプションは --help で確認できます。",
    ),
    "bedrockchat · /help 查看命令 · Ctrl+G 粘贴截图 · Esc+Enter 换行": (
        "bedrockchat · /help for commands · Ctrl+G paste image · Esc+Enter newline",
        "bedrockchat · /help コマンド一覧 · Ctrl+G 画像を貼り付け · Esc+Enter 改行",
    ),
    "未配置模型；用 /model <ID> 设置，或通过 /resume 恢复已有会话。": (
        "No model configured. Use /model <ID> or /resume an existing session.",
        "モデルが未設定です。/model <ID> で指定するか、/resume で既存のセッションを再開してください。",
    ),
    "已取消当前操作；/exit 退出。": (
        "Action cancelled. Use /exit to quit.",
        "操作をキャンセルしました。終了するには /exit を入力してください。",
    ),
}
