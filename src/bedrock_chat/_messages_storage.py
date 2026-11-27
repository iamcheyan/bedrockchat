"""English and Japanese messages for configuration and history."""

MESSAGES = {
    # Configuration
    "模型 ID 或 ARN 格式无效。": (
        "Invalid model ID or ARN format.",
        "モデル ID または ARN の形式が正しくありません。",
    ),
    "不支持 Prompt Management ARN；请使用模型 ID 或推理配置文件 ID/ARN。": (
        "Prompt Management ARNs are not supported. Use a model ID or inference profile ID/ARN.",
        "Prompt Management ARN はサポートされていません。モデル ID または推論プロファイルの ID/ARN を使用してください。",
    ),
    "界面语言必须是 zh、en 或 ja。": (
        "The UI language must be zh, en, or ja.",
        "表示言語には zh、en、ja のいずれかを指定してください。",
    ),
    "{label} 配置格式有误或含有未知字段。不要在配置中填写凭证。": (
        "Invalid configuration or unknown fields in {label}. Do not put credentials in the configuration file.",
        "{label} の設定形式が正しくないか、不明な項目が含まれています。設定ファイルに認証情報を記入しないでください。",
    ),
    "{label} 必须是有效的字符串。": (
        "{label} must be a valid string.",
        "{label} には有効な文字列を指定してください。",
    ),
    "{label} 必须是 true 或 false。": (
        "{label} must be true or false.",
        "{label} には true または false を指定してください。",
    ),
    "{label} 必须是 {low}～{high} 内的整数。": (
        "{label} must be an integer from {low} to {high}.",
        "{label} には {low}～{high} の整数を指定してください。",
    ),
    "模型价格必须是非负数字。": (
        "Model prices must be nonnegative numbers.",
        "モデル料金には 0 以上の数値を指定してください。",
    ),
    "模型价格必须是 0～1000000 内的有限数字。": (
        "Model prices must be finite numbers from 0 to 1000000.",
        "モデル料金には 0～1000000 の有限な数値を指定してください。",
    ),
    "找不到指定的配置文件。": (
        "The specified configuration file was not found.",
        "指定された設定ファイルが見つかりません。",
    ),
    "无法读取配置文件，请检查文件权限与 TOML 语法。": (
        "Could not read the configuration file. Check file permissions and TOML syntax.",
        "設定ファイルを読み込めません。ファイルの権限と TOML の構文を確認してください。",
    ),
    "顶层": ("the top-level table", "トップレベル"),
    "模型别名必须为 1～64 个英文字母、数字、点、下划线或连字符。": (
        "Model aliases must be 1–64 characters: ASCII letters, digits, periods, underscores, or hyphens.",
        "モデルの別名には半角英数字、ピリオド、アンダースコア、ハイフンを 1～64 文字で指定してください。",
    ),
    "模型": ("Model", "モデル"),
    "模型 ID": ("Model ID", "モデル ID"),
    "不同模型别名不能使用同一个模型 ID。": (
        "Different model aliases cannot use the same model ID.",
        "異なる別名に同じモデル ID は指定できません。",
    ),
    "输入与输出价格需同时填写。": (
        "Input and output prices must be set together.",
        "入力料金と出力料金は両方指定してください。",
    ),
    "AWS Region 格式无效。": (
        "Invalid AWS Region format.",
        "AWS リージョンの形式が正しくありません。",
    ),
    # History diagnostics
    "session 与 turn 必须是 Session/Turn 实例。": (
        "session and turn must be instances of Session and Turn, respectively.",
        "session は Session、turn は Turn のインスタンスである必要があります。",
    ),
    "session 必须是 Session 实例。": (
        "session must be a Session instance.",
        "session は Session のインスタンスである必要があります。",
    ),
    "历史记录已禁用，无法重命名会话。": (
        "History is disabled; cannot rename the session.",
        "履歴が無効なため、セッション名を変更できません。",
    ),
    "历史记录已禁用，无法删除会话。": (
        "History is disabled; cannot delete the session.",
        "履歴が無効なため、セッションを削除できません。",
    ),
    "找不到该会话。": ("Session not found.", "セッションが見つかりません。"),
    "会话路径不是目录。": (
        "The session path is not a directory.",
        "セッションのパスはディレクトリではありません。",
    ),
    "历史记录已禁用，无法读取会话。": (
        "History is disabled; cannot load the session.",
        "履歴が無効なため、セッションを読み込めません。",
    ),
    "历史记录已禁用，无法列出会话。": (
        "History is disabled; cannot list sessions.",
        "履歴が無効なため、セッションの一覧を表示できません。",
    ),
    "query 必须是字符串。": ("query must be a string.", "query は文字列である必要があります。"),
    "turn_number 与 image_number 必须是整数。": (
        "turn_number and image_number must be integers.",
        "turn_number と image_number は整数である必要があります。",
    ),
    "轮次超出范围。": ("Turn number is out of range.", "ターン番号が範囲外です。"),
    "附件序号超出范围。": (
        "Attachment number is out of range.",
        "添付ファイルの番号が範囲外です。",
    ),
    "该附件未保存到磁盘（save_images 未启用）。": (
        "This attachment was not saved to disk (save_images is disabled).",
        "この添付ファイルはディスクに保存されていません（save_images が無効です）。",
    ),
    "附件路径无效。": ("Invalid attachment path.", "添付ファイルのパスが無効です。"),
    "附件目录不存在。": (
        "The attachments directory does not exist.",
        "添付ファイルのディレクトリが存在しません。",
    ),
    "无法读取附件目录。": (
        "Could not read the attachments directory.",
        "添付ファイルのディレクトリを読み込めません。",
    ),
    "附件路径不是普通目录。": (
        "The attachments path is not a regular directory.",
        "添付ファイルのパスは通常のディレクトリではありません。",
    ),
    "无法读取附件。": ("Could not read the attachment.", "添付ファイルを読み込めません。"),
    "附件不是普通文件。": (
        "The attachment is not a regular file.",
        "添付ファイルが通常のファイルではありません。",
    ),
    "导出目标已存在，未覆盖。": (
        "The export destination already exists and was not overwritten.",
        "エクスポート先にファイルがあるため、上書きしませんでした。",
    ),
    "导出目录不存在。": (
        "The export directory does not exist.",
        "エクスポート先のディレクトリが存在しません。",
    ),
    "无法写入导出文件。": (
        "Could not write the export file.",
        "エクスポートファイルに書き込めません。",
    ),
    "无法设置导出文件权限。": (
        "Could not set export file permissions.",
        "エクスポートファイルの権限を設定できません。",
    ),
    "历史目录不是普通目录。": (
        "The history directory is not a regular directory.",
        "履歴の保存先は通常のディレクトリではありません。",
    ),
    "历史目录不可读写。": (
        "The history directory is not readable or writable.",
        "履歴ディレクトリを読み書きできません。",
    ),
    "无法创建历史目录。": (
        "Could not create the history directory.",
        "履歴ディレクトリを作成できません。",
    ),
    "会话路径越界。": (
        "The session path is outside the history directory.",
        "セッションのパスが履歴ディレクトリの外を指しています。",
    ),
    "会话路径不是普通目录。": (
        "The session path is not a regular directory.",
        "セッションのパスは通常のディレクトリではありません。",
    ),
    "无法创建会话目录。": (
        "Could not create the session directory.",
        "セッションのディレクトリを作成できません。",
    ),
    "无法读取历史目录。": (
        "Could not read the history directory.",
        "履歴ディレクトリを読み込めません。",
    ),
    "会话 ID 或前缀格式无效。": (
        "Invalid session ID or prefix format.",
        "セッション ID またはその接頭辞の形式が正しくありません。",
    ),
    "会话前缀不唯一，请提供更多字符。": (
        "The prefix matches multiple sessions. Enter more characters.",
        "複数のセッションが一致します。ID の先頭からさらに入力してください。",
    ),
    "无法创建会话锁文件。": (
        "Could not create the session lock file.",
        "セッションのロックファイルを作成できません。",
    ),
    "会话锁文件不是普通文件。": (
        "The session lock file is not a regular file.",
        "セッションのロックファイルは通常のファイルではありません。",
    ),
    "无法设置会话锁文件权限。": (
        "Could not set session lock file permissions.",
        "セッションのロックファイルの権限を設定できません。",
    ),
    "无法读取历史记录。": ("Could not read the history records.", "履歴を読み込めません。"),
    "历史记录不是普通文件。": (
        "The history records are not a regular file.",
        "履歴の保存先は通常のファイルではありません。",
    ),
    "历史记录已损坏。": ("The history records are corrupted.", "履歴が破損しています。"),
    "图片数据数量与附件数量不一致。": (
        "The number of image payloads does not match the number of attachments.",
        "画像データの数と添付ファイルの数が一致しません。",
    ),
    "图片数据必须是 bytes。": (
        "Image data must be bytes.",
        "画像データはバイト列である必要があります。",
    ),
    "无法检查附件路径。": (
        "Could not check the attachment path.",
        "添付ファイルのパスを確認できません。",
    ),
    "附件路径不是普通文件。": (
        "The attachment path is not a regular file.",
        "添付ファイルのパスは通常のファイルではありません。",
    ),
    "无法创建附件目录。": (
        "Could not create the attachments directory.",
        "添付ファイルのディレクトリを作成できません。",
    ),
    "无法写入历史记录。": ("Could not write the history records.", "履歴を書き込めません。"),
    "无法设置历史文件权限。": (
        "Could not set history file permissions.",
        "履歴ファイルの権限を設定できません。",
    ),
    "无法读取会话状态。": (
        "Could not read the session state.",
        "セッションの状態を読み込めません。",
    ),
    "会话状态不是普通文件。": (
        "The session state is not a regular file.",
        "セッション状態の保存先は通常のファイルではありません。",
    ),
    "会话状态已损坏。": ("The session state is corrupted.", "セッションの状態が破損しています。"),
    "无法设置历史目录权限。": (
        "Could not set history directory permissions.",
        "履歴ディレクトリの権限を設定できません。",
    ),
    "无法打开历史目录。": (
        "Could not open the history directory.",
        "履歴ディレクトリを開けません。",
    ),
    "无法写入历史文件。": ("Could not write the history file.", "履歴ファイルに書き込めません。"),
    "会话 ID 必须是 32 位小写十六进制字符串。": (
        "The session ID must be a 32-character lowercase hexadecimal string.",
        "セッション ID は小文字の 16 進数 32 桁で指定してください。",
    ),
    "会话名称必须是 1～200 字符的文本。": (
        "The session name must be 1–200 characters.",
        "セッション名は 1～200 文字で指定してください。",
    ),
    "会话名称不能包含控制字符。": (
        "The session name cannot contain control characters.",
        "セッション名に制御文字は使用できません。",
    ),
    "model_id 必须是非空字符串或 None。": (
        "model_id must be a nonempty string or None.",
        "model_id は空でない文字列または None である必要があります。",
    ),
    "created_at 必须是有效的 UTC 时间字符串。": (
        "created_at must be a valid UTC timestamp string.",
        "created_at は有効な UTC 日時の文字列である必要があります。",
    ),
    "{label} 必须是非负整数。": (
        "{label} must be a nonnegative integer.",
        "{label} は 0 以上の整数である必要があります。",
    ),
    "附件必须是 Attachment 实例。": (
        "Attachments must be Attachment instances.",
        "添付ファイルは Attachment のインスタンスである必要があります。",
    ),
    "附件名称无效。": ("Invalid attachment name.", "添付ファイル名が無効です。"),
    "附件格式无效。": ("Invalid attachment format.", "添付ファイルの形式が無効です。"),
    "附件宽度": ("Attachment width", "添付画像の幅"),
    "附件高度": ("Attachment height", "添付画像の高さ"),
    "附件大小": ("Attachment size", "添付ファイルのサイズ"),
    "usage 必须完整包含 input_tokens/output_tokens/total_tokens。": (
        "usage must contain input_tokens, output_tokens, and total_tokens.",
        "usage には input_tokens、output_tokens、total_tokens がすべて必要です。",
    ),
    "turn 必须是 Turn 实例。": (
        "turn must be a Turn instance.",
        "turn は Turn のインスタンスである必要があります。",
    ),
    "model_id 必须是非空字符串。": (
        "model_id must be a nonempty string.",
        "model_id は空でない文字列である必要があります。",
    ),
    "用户消息必须是字符串。": (
        "The user message must be a string.",
        "ユーザーメッセージは文字列である必要があります。",
    ),
    "助手回复必须是字符串。": (
        "The assistant reply must be a string.",
        "アシスタントの応答は文字列である必要があります。",
    ),
    "status 格式无效。": ("Invalid status format.", "status の形式が正しくありません。"),
    "timestamp 必须是非空字符串。": (
        "timestamp must be a nonempty string.",
        "timestamp は空でない文字列である必要があります。",
    ),
    # Markdown export
    "- 会话 ID: `{session_id}`": (
        "- Session ID: `{session_id}`",
        "- セッション ID: `{session_id}`",
    ),
    "- 模型: `{model_id}`": ("- Model: `{model_id}`", "- モデル: `{model_id}`"),
    "- 创建时间: {created_at}": ("- Created: {created_at}", "- 作成日時: {created_at}"),
    "- 更新时间: {updated_at}": ("- Updated: {updated_at}", "- 更新日時: {updated_at}"),
    "- 上下文起点: {context_start}": (
        "- Context start: {context_start}",
        "- コンテキスト開始位置: {context_start}",
    ),
    "## 第 {number} 轮（complete）": (
        "## Turn {number} (complete)",
        "## 第 {number} ターン（完了）",
    ),
    "## 第 {number} 轮（未完成: {status}）": (
        "## Turn {number} (Incomplete: {status})",
        "## 第 {number} ターン（未完了: {status}）",
    ),
    "模型: `{model_id}`": ("Model: `{model_id}`", "モデル: `{model_id}`"),
    "时间: {timestamp}": ("Time: {timestamp}", "日時: {timestamp}"),
    "### 用户": ("### User", "### ユーザー"),
    "### 助手": ("### Assistant", "### アシスタント"),
    "### 附件": ("### Attachments", "### 添付ファイル"),
    "{format} {width}×{height}，{size} 字节": (
        "{format} {width}×{height}, {size} bytes",
        "{format} {width}×{height}、{size} バイト",
    ),
    "未保存（save_images 未启用）": (
        "Not saved (save_images is disabled)",
        "未保存（save_images が無効）",
    ),
    "- `{name}`（{detail}）— {location}": (
        "- `{name}` ({detail}) — {location}",
        "- `{name}`（{detail}）— {location}",
    ),
    "用量: 未提供": ("Usage: not reported", "使用量: 報告なし"),
    "用量: {usage}": ("Usage: {usage}", "使用量: {usage}"),
    "## 汇总": ("## Summary", "## 集計"),
    "- 完整回复: {complete} 轮": (
        "- Turns with complete replies: {complete}",
        "- 応答が完了したターン数: {complete}",
    ),
    "- 未完成/失败: {incomplete} 轮": (
        "- Incomplete or failed turns: {incomplete}",
        "- 未完了または失敗したターン数: {incomplete}",
    ),
    "- 输入 tokens: {input_tokens}，输出 tokens: {output_tokens}（仅统计有用量的轮次）": (
        "- Input tokens: {input_tokens}, output tokens: {output_tokens} (only turns with reported usage)",
        "- 入力トークン: {input_tokens}、出力トークン: {output_tokens}（使用量が報告されたターンのみ）",
    ),
}
