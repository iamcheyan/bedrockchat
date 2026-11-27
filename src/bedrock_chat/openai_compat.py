"""Send explicit chat requests to an OpenAI-compatible Chat Completions endpoint."""

import json
from base64 import b64encode
from collections.abc import Callable, Iterator
from contextlib import closing
from http.client import HTTPConnection, HTTPException, HTTPResponse, HTTPSConnection
from urllib.parse import urlsplit

from .bedrock import SendResult, Usage
from .i18n import tr

_CONNECT_TIMEOUT = 5
_IO_TIMEOUT = 60
_MAX_RESPONSE_BYTES = 8 * 1024 * 1024

_REQUEST_ERROR = "OpenAI 兼容请求格式无效。"
_RESPONSE_ERROR = "OpenAI 兼容服务返回了无效的回复。"
_INCOMPLETE = "OpenAI 兼容回复未正常结束或未返回可识别的停止原因。"
_TOO_LARGE = "OpenAI 兼容回复超出大小限制。"
_TIMEOUT = "连接 OpenAI 兼容服务或读取回复超时。"
_NETWORK = "OpenAI 兼容请求失败，请检查端点和网络设置。"
_TOOL_ERROR = "模型请求调用工具；本客户端不支持工具调用。"
_STOP_REASONS = {
    "stop": ("end_turn", None),
    "length": ("max_tokens", None),
    "tool_calls": ("tool_use", _TOOL_ERROR),
    "function_call": ("tool_use", _TOOL_ERROR),
    "content_filter": ("content_filtered", "OpenAI 兼容服务的内容过滤器阻止了回复。"),
}
_IMAGE_MIME = {"png": "image/png", "jpeg": "image/jpeg"}


class _Problem(Exception):
    """A fixed, safe diagnostic, never text from the service."""


def _text_block(block: dict) -> bool:
    return isinstance(block, dict) and block.keys() == {"text"} and isinstance(block["text"], str)


def _content(blocks: list, *, allow_images: bool) -> str | list[dict]:
    parts = []
    has_image = False
    for block in blocks:
        if _text_block(block):
            parts.append({"type": "text", "text": block["text"]})
            continue
        if not allow_images or block.keys() != {"image"}:
            raise _Problem(_REQUEST_ERROR)
        image = block["image"]
        if image.keys() != {"format", "source"} or image["source"].keys() != {"bytes"}:
            raise _Problem(_REQUEST_ERROR)
        mime = _IMAGE_MIME[image["format"]]
        raw = image["source"]["bytes"]
        if not isinstance(raw, bytes) or not raw:
            raise _Problem(_REQUEST_ERROR)
        encoded = b64encode(raw).decode("ascii")
        parts.append({"type": "image_url", "image_url": {"url": f"data:{mime};base64,{encoded}"}})
        has_image = True
    return parts if has_image else "".join(part["text"] for part in parts)


def _payload(request: dict, stream: bool) -> dict:
    if request.keys() - {"modelId", "messages", "system", "inferenceConfig"}:
        raise _Problem(_REQUEST_ERROR)
    model = request["modelId"]
    inference = request["inferenceConfig"]
    if not isinstance(model, str) or not model.strip() or inference.keys() != {"maxTokens"}:
        raise _Problem(_REQUEST_ERROR)
    max_tokens = inference["maxTokens"]
    if type(max_tokens) is not int or max_tokens < 1:
        raise _Problem(_REQUEST_ERROR)

    messages = []
    if system := request.get("system"):
        messages.append({"role": "system", "content": _content(system, allow_images=False)})
    original = request["messages"]
    for index, message in enumerate(original):
        if message.keys() != {"role", "content"} or message["role"] not in ("user", "assistant"):
            raise _Problem(_REQUEST_ERROR)
        role = message["role"]
        content = _content(
            message["content"], allow_images=role == "user" and index == len(original) - 1
        )
        messages.append({"role": role, "content": content})
    payload = {
        "model": model,
        "messages": messages,
        "max_tokens": max_tokens,
        "stream": stream,
    }
    if stream:
        payload["stream_options"] = {"include_usage": True}
    return payload


def _read_usage(event: dict) -> Usage | None:
    if isinstance(usage := event.get("usage"), dict):
        values = [usage.get(key) for key in ("prompt_tokens", "completion_tokens", "total_tokens")]
        if all(type(value) is int and value >= 0 for value in values):
            return Usage(*values)
    return None


def _sse_data(response: HTTPResponse) -> Iterator[bytes]:
    remaining = _MAX_RESPONSE_BYTES
    data = bytearray()
    first = True
    while line := response.readline(remaining + 1):
        remaining -= len(line)
        if remaining < 0:
            raise _Problem(_TOO_LARGE)
        if first:
            line = line.removeprefix(b"\xef\xbb\xbf")
            first = False
        line = line.rstrip(b"\r\n")
        if not line:
            if data:
                yield bytes(data[:-1])
                data.clear()
        else:
            field, _, value = line.partition(b":")
            if field == b"data":
                data.extend(value.removeprefix(b" "))
                data.append(10)  # SSE joins data fields with a newline.


def _json_body(response: HTTPResponse) -> bytes:
    raw = response.read(_MAX_RESPONSE_BYTES + 1)
    if len(raw) > _MAX_RESPONSE_BYTES:
        raise _Problem(_TOO_LARGE)
    if response.length:
        raise _Problem(_INCOMPLETE)
    return raw


class OpenAICompatible:
    """Synchronous, unauthenticated client for a validated, complete endpoint URL."""

    def __init__(self, endpoint: str, *, stream: bool = True):
        self._endpoint = urlsplit(endpoint)
        self._stream = stream

    def send(self, request: dict, on_text: Callable[[str], None]) -> SendResult:
        chunks: list[str] = []
        usage: Usage | None = None
        stop = "error"
        error = None
        try:
            try:
                body = json.dumps(_payload(request, self._stream)).encode("utf-8")
            except (KeyError, TypeError, ValueError, AttributeError):
                raise _Problem(_REQUEST_ERROR) from None

            endpoint = self._endpoint
            connection = HTTPSConnection if endpoint.scheme == "https" else HTTPConnection
            target = endpoint.path or "/"
            if endpoint.query:
                target += "?" + endpoint.query
            # Direct stdlib connection: no ambient proxies, redirects or retries.
            with closing(
                connection(endpoint.hostname, endpoint.port, timeout=_CONNECT_TIMEOUT)
            ) as conn:
                conn.connect()
                conn.sock.settimeout(_IO_TIMEOUT)
                accept = (
                    "text/event-stream, application/json" if self._stream else "application/json"
                )
                conn.request(
                    "POST",
                    target,
                    body=body,
                    headers={"Content-Type": "application/json", "Accept": accept},
                )
                with conn.getresponse() as response:
                    if not 200 <= response.status < 300:
                        return SendResult(
                            "",
                            None,
                            "error",
                            tr("OpenAI 兼容请求失败（HTTP {status}）。", status=response.status),
                        )
                    if response.length is not None and response.length > _MAX_RESPONSE_BYTES:
                        raise _Problem(_TOO_LARGE)
                    is_sse = response.headers.get_content_type() == "text/event-stream"
                    bodies = _sse_data(response) if is_sse else (_json_body(response),)
                    finished = not is_sse
                    reason = None
                    for raw in bodies:
                        if is_sse and raw.strip() == b"[DONE]":
                            finished = True
                            break
                        event = json.loads(raw.decode("utf-8"))
                        if not isinstance(event, dict) or "error" in event:
                            raise _Problem(_RESPONSE_ERROR)
                        if (new_usage := _read_usage(event)) is not None:
                            usage = new_usage
                        choices = event.get("choices", [])
                        if not isinstance(choices, list):
                            raise _Problem(_RESPONSE_ERROR)
                        if not choices:
                            continue
                        choice = choices[0]
                        if not isinstance(choice, dict):
                            raise _Problem(_RESPONSE_ERROR)
                        content = choice.get("delta", {}) if is_sse else choice.get("message")
                        if not isinstance(content, dict):
                            raise _Problem(_RESPONSE_ERROR)
                        if isinstance(text := content.get("content"), str) and text:
                            chunks.append(text)
                            on_text(text)
                        if choice.get("finish_reason") is not None:
                            reason = choice["finish_reason"]
                    if not finished or not isinstance(reason, str) or reason not in _STOP_REASONS:
                        raise _Problem(_INCOMPLETE)
                    stop, message = _STOP_REASONS[reason]
                    error = tr(message) if message else None
        except KeyboardInterrupt:
            stop, error = "interrupted", tr("已中断。")
        except _Problem as exc:
            stop, error = "error", tr(str(exc))
        except TimeoutError:
            stop, error = "error", tr(_TIMEOUT)
        except (OSError, HTTPException):
            stop, error = "error", tr(_NETWORK)
        except (ValueError, RecursionError):
            stop, error = "error", tr(_RESPONSE_ERROR)
        return SendResult("".join(chunks), usage, stop, error)
