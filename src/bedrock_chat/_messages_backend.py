"""English and Japanese translations for Bedrock and image diagnostics."""

MESSAGES = {
    "请指定有效的 Bedrock 模型 ID 或推理配置文件 ID/ARN。": (
        "Specify a valid Bedrock model ID or inference profile ID/ARN.",
        "有効な Bedrock モデル ID または推論プロファイルの ID/ARN を指定してください。",
    ),
    "输入与输出 Token 上限必须是正整数。": (
        "Input and output token limits must be positive integers.",
        "入力と出力のトークン上限には正の整数を指定してください。",
    ),
    "请输入消息或添加图片。": (
        "Enter a message or attach an image.",
        "メッセージを入力するか、画像を添付してください。",
    ),
    "图片尺寸必须是正整数。": (
        "Image dimensions must be positive integers.",
        "画像の幅と高さには正の整数を指定してください。",
    ),
    "图片必须是有效的 Bedrock 内联字节格式。": (
        "Images must be supplied as valid inline bytes for Bedrock.",
        "画像は Bedrock に対応するインラインのバイトデータで指定してください。",
    ),
    "当前消息（含图片和系统提示词）超出输入 Token 预算："
    "估算 {current_cost}，上限 {max_input_tokens}。": (
        "The current message (including images and the system prompt) exceeds the input token "
        "budget: estimated {current_cost}, limit {max_input_tokens}.",
        "現在のメッセージ（画像とシステムプロンプトを含む）が入力トークン予算を超えています。"
        "推定 {current_cost}、上限 {max_input_tokens}。",
    ),
    "历史轮次必须包含用户消息和助手回复的文本。": (
        "Each turn in the history must include both a user message and an assistant reply as text.",
        "履歴の各ターンには、テキストのユーザーメッセージとアシスタントの応答が必要です。",
    ),
    "不支持工具调用、扩展思考或其他额外的请求字段。": (
        "Tool calls, extended thinking, and other extra request fields are not supported.",
        "ツール呼び出し、拡張思考、その他の追加リクエストフィールドには対応していません。",
    ),
    "系统提示词仅支持文本。": (
        "System prompts support text only.",
        "システムプロンプトにはテキストのみ指定できます。",
    ),
    "消息仅支持文本，且只能在当前用户消息中附加内联图片。": (
        "Messages support text only, except that inline images may be attached to the current "
        "user message.",
        "メッセージはテキストのみ対応しています。インライン画像は現在のユーザーメッセージにのみ添付できます。",
    ),
    "AWS 凭证无效或已过期；若使用 SSO，请重新运行 aws sso login 并选择对应的 profile。": (
        "AWS credentials are invalid or have expired. If you use SSO, run aws sso login again "
        "with the appropriate profile.",
        "AWS の認証情報が無効か、有効期限が切れています。SSO を使用している場合は、"
        "該当するプロファイルを指定して aws sso login を再実行してください。",
    ),
    "Bedrock 请求失败，请检查配置、凭证和网络。": (
        "The Bedrock request failed. Check your configuration, credentials, and network connection.",
        "Bedrock リクエストが失敗しました。設定、認証情報、ネットワーク接続を確認してください。",
    ),
    "Bedrock 流未正常结束或未返回可识别的停止原因。": (
        "The Bedrock stream ended unexpectedly or did not return a recognized stop reason.",
        "Bedrock のストリームが正常に終了しなかったか、認識できる停止理由が返されませんでした。",
    ),
    "访问被拒绝，请检查 IAM 权限及 Bedrock 模型访问设置。": (
        "Access denied. Check IAM permissions and the Bedrock model access settings.",
        "アクセスが拒否されました。IAM 権限と Bedrock のモデルアクセス設定を確認してください。",
    ),
    "Bedrock 服务内部错误，请稍后再试。": (
        "Bedrock encountered an internal service error. Try again later.",
        "Bedrock サービスで内部エラーが発生しました。しばらくしてから再試行してください。",
    ),
    "模型处理失败，请检查所选模型。": (
        "The model could not process the request. Check the selected model.",
        "モデルがリクエストを処理できませんでした。選択したモデルを確認してください。",
    ),
    "模型尚未就绪，请稍后再试。": (
        "The model is not ready. Try again later.",
        "モデルの準備ができていません。しばらくしてから再試行してください。",
    ),
    "Bedrock 模型流中断，请稍后再试。": (
        "The Bedrock model stream was interrupted. Try again later.",
        "Bedrock モデルのストリームが中断されました。しばらくしてから再試行してください。",
    ),
    "模型响应超时，请稍后再试。": (
        "The model response timed out. Try again later.",
        "モデルの応答がタイムアウトしました。しばらくしてから再試行してください。",
    ),
    "找不到模型，请检查模型 ID、推理配置文件和区域。": (
        "Model not found. Check the model ID, inference profile, and region.",
        "モデルが見つかりません。モデル ID、推論プロファイル、リージョンを確認してください。",
    ),
    "超出 Bedrock 服务配额。": (
        "The Bedrock service quota was exceeded.",
        "Bedrock のサービスクォータを超えました。",
    ),
    "Bedrock 服务暂不可用，请稍后再试。": (
        "Bedrock is temporarily unavailable. Try again later.",
        "Bedrock サービスは一時的に利用できません。しばらくしてから再試行してください。",
    ),
    "Bedrock 请求被限流，请稍后再试。": (
        "Bedrock throttled the request. Try again later.",
        "Bedrock のリクエストがスロットリングされました。しばらくしてから再試行してください。",
    ),
    "模型请求调用工具；本客户端不支持工具调用。": (
        "The model requested a tool call, but this client does not support tool calls.",
        "モデルがツールの呼び出しを要求しましたが、このクライアントはツール呼び出しに対応していません。",
    ),
    "Bedrock 护栏阻止了回复。": (
        "A Bedrock guardrail blocked the response.",
        "Bedrock のガードレールが応答をブロックしました。",
    ),
    "Bedrock 内容过滤器阻止了回复。": (
        "The Bedrock content filter blocked the response.",
        "Bedrock のコンテンツフィルターが応答をブロックしました。",
    ),
    "Bedrock 模型输出格式无效。": (
        "The Bedrock model output is in an invalid format.",
        "Bedrock モデルの出力形式が無効です。",
    ),
    "模型返回了无效的工具调用；本客户端不支持工具调用。": (
        "The model returned an invalid tool call. This client does not support tool calls.",
        "モデルが無効なツール呼び出しを返しました。このクライアントはツール呼び出しに対応していません。",
    ),
    "超过模型实际的上下文窗口，请缩减输入。": (
        "The input exceeds the model's context window. Shorten the input.",
        "モデルのコンテキストウィンドウの上限を超えました。入力を短くしてください。",
    ),
    "Bedrock 未通过请求校验，请检查模型 ID、区域和输入参数。": (
        "Bedrock rejected the request. Check the model ID, region, and input parameters.",
        "Bedrock のリクエスト検証に失敗しました。モデル ID、リージョン、入力パラメーターを確認してください。",
    ),
    "Bedrock 未通过请求校验，请检查模型 ID、区域和输入参数。"
    "请确认该模型支持视觉（图片）输入，且图片符合模型限制。": (
        "Bedrock rejected the request. Check the model ID, region, and input parameters. "
        "Make sure the model supports image input and the images meet its limits.",
        "Bedrock のリクエスト検証に失敗しました。モデル ID、リージョン、入力パラメーターを確認してください。"
        "モデルが画像入力に対応しており、画像がモデルの制限を満たしていることも確認してください。",
    ),
    "找不到 AWS 配置文件，请检查 profile 名称。": (
        "AWS profile not found. Check the profile name.",
        "AWS プロファイルが見つかりません。プロファイル名を確認してください。",
    ),
    "未指定 AWS 区域，请设置 --region、AWS_REGION 或 AWS_DEFAULT_REGION。": (
        "No AWS region specified. Set --region, AWS_REGION, or AWS_DEFAULT_REGION.",
        "AWS リージョンが指定されていません。--region、AWS_REGION、または AWS_DEFAULT_REGION を設定してください。",
    ),
    "缺少可用的 AWS 凭证，请检查凭证配置；若使用 SSO，请先运行 aws sso login。": (
        "No usable AWS credentials found. Check your credential configuration. If you use SSO, "
        "run aws sso login first.",
        "利用可能な AWS 認証情報が見つかりません。認証情報の設定を確認してください。"
        "SSO を使用している場合は、先に aws sso login を実行してください。",
    ),
    "连接 Bedrock 或读取回复超时，请检查网络后再试。": (
        "Connecting to Bedrock or reading the response timed out. Check your network and try again.",
        "Bedrock への接続または応答の読み取りがタイムアウトしました。ネットワークを確認して再試行してください。",
    ),
    "Bedrock 网络连接失败，请检查网络和 AWS 区域。": (
        "Could not connect to Bedrock. Check your network and AWS region.",
        "Bedrock に接続できませんでした。ネットワークと AWS リージョンを確認してください。",
    ),
    "已中断。": ("Interrupted.", "中断しました。"),
    "图片边长限制必须为 1～{max_edge} 像素。": (
        "The image edge limit must be between 1 and {max_edge} pixels.",
        "画像の辺の長さの上限は 1～{max_edge} ピクセルにしてください。",
    ),
    "图片大小限制必须为 1～{max_bytes} 字节。": (
        "The image size limit must be between 1 and {max_bytes} bytes.",
        "画像のファイルサイズの上限は 1～{max_bytes} バイトにしてください。",
    ),
    "原始图片的像素数超出限制。": (
        "The source image exceeds the pixel limit.",
        "元の画像の画素数が上限を超えています。",
    ),
    "不支持多帧图片。": (
        "Multi-frame images are not supported.",
        "複数フレームの画像には対応していません。",
    ),
    "图片无法解码，请使用有效的 PNG 或 JPEG 图片。": (
        "Could not decode the image. Use a valid PNG or JPEG image.",
        "画像をデコードできませんでした。有効な PNG または JPEG 画像を使用してください。",
    ),
    "无法编码图片。": ("Could not encode the image.", "画像をエンコードできませんでした。"),
    "图片无法缩小到指定的字节限制。": (
        "The image could not be reduced to fit the specified byte limit.",
        "指定されたバイト数の上限まで画像を縮小できませんでした。",
    ),
    "图片路径必须指向普通文件。": (
        "The image path must point to a regular file.",
        "画像のパスには通常のファイルを指定してください。",
    ),
    "原始图片文件太大。": (
        "The source image file is too large.",
        "元の画像ファイルが大きすぎます。",
    ),
    "无法读取图片文件。": (
        "Could not read the image file.",
        "画像ファイルを読み取れませんでした。",
    ),
    "剪贴板图片太大。": (
        "The clipboard image is too large.",
        "クリップボードの画像が大きすぎます。",
    ),
    "读取 Windows 剪贴板超时，请重试。": (
        "Reading the Windows clipboard timed out. Try again.",
        "Windows のクリップボードの読み取りがタイムアウトしました。再試行してください。",
    ),
    "无法启动 Windows PowerShell，请检查 WSL 互操作是否可用。": (
        "Could not start Windows PowerShell. Check whether WSL interoperability is available.",
        "Windows PowerShell を起動できませんでした。WSL の相互運用機能が利用可能か確認してください。",
    ),
    "Windows 剪贴板中没有图片。": (
        "The Windows clipboard does not contain an image.",
        "Windows のクリップボードに画像がありません。",
    ),
    "无法读取剪贴板，请重试。": (
        "Could not read the clipboard. Try again.",
        "クリップボードを読み取れませんでした。再試行してください。",
    ),
}
