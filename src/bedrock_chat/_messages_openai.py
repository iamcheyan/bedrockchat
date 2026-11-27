"""English and Japanese translations for OpenAI-compatible transport diagnostics."""

MESSAGES = {
    "OpenAI 兼容请求格式无效。": (
        "The OpenAI-compatible request is invalid.",
        "OpenAI 互換リクエストの形式が無効です。",
    ),
    "OpenAI 兼容服务返回了无效的回复。": (
        "The OpenAI-compatible service returned an invalid response.",
        "OpenAI 互換サービスから無効な応答が返されました。",
    ),
    "OpenAI 兼容回复未正常结束或未返回可识别的停止原因。": (
        "The OpenAI-compatible response ended unexpectedly or gave no recognized stop reason.",
        "OpenAI 互換サービスの応答が正常に終了しなかったか、認識できる停止理由が返されませんでした。",
    ),
    "OpenAI 兼容回复超出大小限制。": (
        "The OpenAI-compatible response exceeded the size limit.",
        "OpenAI 互換サービスの応答がサイズ上限を超えました。",
    ),
    "连接 OpenAI 兼容服务或读取回复超时。": (
        "Connecting to the OpenAI-compatible service or reading its response timed out.",
        "OpenAI 互換サービスへの接続または応答の読み取りがタイムアウトしました。",
    ),
    "OpenAI 兼容请求失败，请检查端点和网络设置。": (
        "The OpenAI-compatible request failed. Check the endpoint and network settings.",
        "OpenAI 互換リクエストに失敗しました。エンドポイントとネットワーク設定を確認してください。",
    ),
    "OpenAI 兼容服务的内容过滤器阻止了回复。": (
        "The OpenAI-compatible service's content filter blocked the response.",
        "OpenAI 互換サービスのコンテンツフィルターが応答をブロックしました。",
    ),
    "OpenAI 兼容请求失败（HTTP {status}）。": (
        "The OpenAI-compatible request failed (HTTP {status}).",
        "OpenAI 互換リクエストに失敗しました（HTTP {status}）。",
    ),
}
