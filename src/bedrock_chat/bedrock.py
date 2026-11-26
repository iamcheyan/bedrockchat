"""Build local chat requests and stream replies from AWS Bedrock."""

from collections.abc import Callable
from dataclasses import dataclass
from unicodedata import east_asian_width

import boto3
from botocore import exceptions as aws
from botocore.config import Config
from botocore.session import get_session
from urllib3.exceptions import HTTPError, TimeoutError

from .config import ConfigError, validate_model


class ChatError(Exception):
    """A safe, user-facing error in local chat input."""


@dataclass
class Usage:
    input_tokens: int
    output_tokens: int
    total_tokens: int


@dataclass
class SendResult:
    text: str
    usage: Usage | None
    stop_reason: str
    error: str | None


@dataclass
class Budget:
    request: dict
    estimated_tokens: int
    dropped_turns: int


def _text_tokens(text: str) -> int:
    # Round up at 3 ASCII characters/token. Count wide/fullwidth characters
    # (including most CJK) as 2 tokens each and other non-ASCII as 1 each.
    ascii_count = sum(char.isascii() for char in text)
    wide = sum(not char.isascii() and east_asian_width(char) in {"W", "F"} for char in text)
    return (ascii_count + 2) // 3 + len(text) - ascii_count + wide


def _check_model(model_id: str) -> None:
    if not isinstance(model_id, str):
        raise ChatError("请指定有效的 Bedrock 模型 ID 或推理配置文件 ID/ARN。")
    try:
        validate_model(model_id)
    except ConfigError as exc:
        raise ChatError(str(exc)) from None


def _text_message(role: str, text: str) -> dict:
    return {"role": role, "content": [{"text": text}]}


def build_request(
    model_id: str,
    previous: list[tuple[str, str]],
    text: str,
    images: list[tuple[dict, int, int]],
    *,
    max_input_tokens: int,
    max_output_tokens: int,
    system_prompt: str = "",
) -> Budget:
    """Build a request from (user text, assistant text) pairs, oldest first.

    Input cost is estimated locally: 3 tokens per request, 8 per message or
    system prompt, text as in _text_tokens(), and ceil(width*height/750) per
    image. This is a heuristic, not a guarantee of the model's token limit.
    Dimensions are for estimation only and aren't sent. Only current images
    and ordinary text are included; the newest suffix of complete turns wins.
    """
    _check_model(model_id)
    if any(type(limit) is not int or limit < 1 for limit in (max_input_tokens, max_output_tokens)):
        raise ChatError("输入与输出 Token 上限必须是正整数。")
    has_text = bool(text.strip())
    if not has_text and not images:
        raise ChatError("请输入消息或添加图片。")

    current_cost = 3 + 8 + (_text_tokens(text) if has_text else 0)
    if system_prompt:
        current_cost += 8 + _text_tokens(system_prompt)
    content = []
    for payload, width, height in images:
        if type(width) is not int or type(height) is not int or min(width, height) < 1:
            raise ChatError("图片尺寸必须是正整数。")
        if not _is_inline_image(payload):
            raise ChatError("图片必须是有效的 Bedrock 内联字节格式。")
        current_cost += (width * height + 749) // 750
        content.append({"image": payload})
    if has_text:
        content.append({"text": text})
    if current_cost > max_input_tokens:
        raise ChatError(
            f"当前消息（含图片和系统提示词）超出输入 Token 预算："
            f"估算 {current_cost}，上限 {max_input_tokens}。"
        )

    # Work backward so we only estimate the history we might actually send.
    total = current_cost
    dropped = len(previous)
    for user, assistant in reversed(previous):
        if not all(isinstance(value, str) and value.strip() for value in (user, assistant)):
            raise ChatError("历史轮次必须包含用户消息和助手回复的文本。")
        cost = 16 + _text_tokens(user) + _text_tokens(assistant)
        if total + cost > max_input_tokens:
            break
        total += cost
        dropped -= 1

    messages = []
    for user, assistant in previous[dropped:]:
        messages.extend((_text_message("user", user), _text_message("assistant", assistant)))
    messages.append({"role": "user", "content": content})
    request = {
        "modelId": model_id,
        "messages": messages,
        "inferenceConfig": {"maxTokens": max_output_tokens},
    }
    if system_prompt:
        request["system"] = [{"text": system_prompt}]
    return Budget(request, total, dropped)


def _is_inline_image(payload: object) -> bool:
    return (
        isinstance(payload, dict)
        and payload.keys() == {"format", "source"}
        and payload["format"] in ("png", "jpeg", "gif", "webp")
        and isinstance(source := payload["source"], dict)
        and source.keys() == {"bytes"}
        and isinstance(source["bytes"], bytes)
        and bool(source["bytes"])
    )


def _check_request(request: dict) -> bool:
    """Guard the chat-only boundary even if send() is called without the builder."""
    _check_model(request.get("modelId"))
    if request.keys() - {"modelId", "messages", "system", "inferenceConfig"}:
        raise ChatError("不支持工具调用、扩展思考或其他额外的请求字段。")
    has_images = False
    for entry in request.get("system", ()):
        if entry.keys() != {"text"}:
            raise ChatError("系统提示词仅支持文本。")
    messages = request.get("messages", ())
    for index, message in enumerate(messages):
        for block in message["content"]:
            if block.keys() == {"text"}:
                continue
            if (
                block.keys() == {"image"}
                and message["role"] == "user"
                and index == len(messages) - 1
                and _is_inline_image(block["image"])
            ):
                has_images = True
                continue
            raise ChatError("消息仅支持文本，且只能在当前用户消息中附加内联图片。")
    return has_images


_REAUTH = "AWS 凭证无效或已过期；若使用 SSO，请重新运行 aws sso login 并选择对应的 profile。"
_UNKNOWN_ERROR = "Bedrock 请求失败，请检查配置、凭证和网络。"
_INCOMPLETE = "Bedrock 流未正常结束或未返回可识别的停止原因。"

_SERVICE_ERRORS = {
    "AccessDeniedException": "访问被拒绝，请检查 IAM 权限及 Bedrock 模型访问设置。",
    "InternalServerException": "Bedrock 服务内部错误，请稍后再试。",
    "ModelErrorException": "模型处理失败，请检查所选模型。",
    "ModelNotReadyException": "模型尚未就绪，请稍后再试。",
    "ModelStreamErrorException": "Bedrock 模型流中断，请稍后再试。",
    "ModelTimeoutException": "模型响应超时，请稍后再试。",
    "ResourceNotFoundException": "找不到模型，请检查模型 ID、推理配置文件和区域。",
    "ServiceQuotaExceededException": "超出 Bedrock 服务配额。",
    "ServiceUnavailableException": "Bedrock 服务暂不可用，请稍后再试。",
    "ThrottlingException": "Bedrock 请求被限流，请稍后再试。",
}
_STREAM_CODES = {
    code[:1].lower() + code[1:]: code for code in (*_SERVICE_ERRORS, "ValidationException")
}

_SUCCESS_STOPS = {"end_turn", "stop_sequence", "max_tokens"}
_STOP_ERRORS = {
    "tool_use": "模型请求调用工具；本客户端不支持工具调用。",
    "guardrail_intervened": "Bedrock 护栏阻止了回复。",
    "content_filtered": "Bedrock 内容过滤器阻止了回复。",
    "malformed_model_output": "Bedrock 模型输出格式无效。",
    "malformed_tool_use": "模型返回了无效的工具调用；本客户端不支持工具调用。",
    "model_context_window_exceeded": "超过模型实际的上下文窗口，请缩减输入。",
}


def _service_error(code: str, has_images: bool) -> str:
    # Whitelist diagnostics. Never echo or log raw AWS response fields.
    if code and code[:1].islower():
        code = _STREAM_CODES.get(code, code)
    if code == "ValidationException":
        message = "Bedrock 未通过请求校验，请检查模型 ID、区域和输入参数。"
        if has_images:
            message += "请确认该模型支持视觉（图片）输入，且图片符合模型限制。"
        return message
    if code in {
        "ExpiredToken",
        "ExpiredTokenException",
        "InvalidClientTokenId",
        "InvalidSignatureException",
        "UnrecognizedClientException",
    }:
        return _REAUTH
    return _SERVICE_ERRORS.get(code, _UNKNOWN_ERROR)


def _safe_error(exc: Exception, has_images: bool) -> str:
    if isinstance(exc, aws.ClientError):  # Includes botocore's EventStreamError.
        code = exc.response.get("Error", {}).get("Code")
        return _service_error(code, has_images) if isinstance(code, str) else _UNKNOWN_ERROR
    if isinstance(exc, aws.ProfileNotFound):
        return "找不到 AWS 配置文件，请检查 profile 名称。"
    if isinstance(exc, aws.NoRegionError):
        return "未指定 AWS 区域，请设置 region 或 AWS_DEFAULT_REGION。"
    if isinstance(exc, (aws.SSOError, aws.TokenRetrievalError, aws.CredentialRetrievalError)):
        return _REAUTH
    if isinstance(exc, (aws.NoCredentialsError, aws.PartialCredentialsError)):
        return "缺少可用的 AWS 凭证，请检查凭证配置；若使用 SSO，请先运行 aws sso login。"
    if isinstance(exc, (aws.ConnectTimeoutError, aws.ReadTimeoutError, TimeoutError)):
        return "连接 Bedrock 或读取回复超时，请检查网络后再试。"
    if isinstance(exc, (aws.ConnectionError, aws.HTTPClientError, HTTPError, OSError)):
        return "Bedrock 网络连接失败，请检查网络和 AWS 区域。"
    if isinstance(exc, aws.ParamValidationError):
        return _service_error("ValidationException", has_images)
    return _UNKNOWN_ERROR


def _read_usage(metadata: dict) -> Usage | None:
    usage = metadata.get("usage")
    if isinstance(usage, dict):
        values = [usage.get(key) for key in ("inputTokens", "outputTokens", "totalTokens")]
        if all(type(value) is int and value >= 0 for value in values):
            return Usage(*values)
    return None


class Bedrock:
    """Synchronous, lazy ConverseStream client using the boto3 credential chain."""

    def __init__(self, region: str | None = None, profile: str | None = None):
        self._region = region
        self._profile = profile
        self._client = None

    def _make_client(self):
        core_session = get_session()
        # Disable optional SDK client-side monitoring for this session only.
        # AWS account-level model invocation logging is configured separately.
        core_session.set_config_variable("csm_enabled", False)
        core_session.set_config_variable("ignore_configured_endpoint_urls", True)
        session = boto3.Session(
            botocore_session=core_session, region_name=self._region, profile_name=self._profile
        )
        # Use SDK endpoint resolution and default TLS verification.
        return session.client(
            "bedrock-runtime",
            config=Config(
                ignore_configured_endpoint_urls=True,
                retries={"total_max_attempts": 1},
                connect_timeout=5,
                read_timeout=60,
            ),
        )

    def send(self, request: dict, on_text: Callable[[str], None]) -> SendResult:
        """Stream text only, preserving partial output and usage on failure."""
        chunks: list[str] = []
        usage = None
        stop = "error"
        error = None
        stream = None
        has_images = False
        try:
            try:
                has_images = _check_request(request)
            except ChatError as exc:
                return SendResult("", None, "error", str(exc))
            if self._client is None:
                self._client = self._make_client()
            stream = self._client.converse_stream(**request)["stream"]
            for event in stream:
                if "contentBlockDelta" in event:
                    delta = event["contentBlockDelta"]["delta"]
                    # The delta is a tagged union. Never display or retain
                    # reasoningContent, toolUse, or other non-text fields.
                    if (
                        delta.keys() == {"text"}
                        and isinstance(chunk := delta["text"], str)
                        and chunk
                    ):
                        chunks.append(chunk)
                        on_text(chunk)
                elif "messageStop" in event:
                    reason = event["messageStop"].get("stopReason")
                    if isinstance(reason, str) and (
                        reason in _SUCCESS_STOPS or reason in _STOP_ERRORS
                    ):
                        stop = reason
                elif "metadata" in event:
                    new_usage = _read_usage(event["metadata"])
                    if new_usage is not None:
                        usage = new_usage
                elif code := next(
                    (_STREAM_CODES[key] for key in event if key in _STREAM_CODES), None
                ):
                    stop, error = "error", _service_error(code, has_images)
                    break
            if error is None:
                if stop == "error":
                    error = _INCOMPLETE
                else:
                    error = _STOP_ERRORS.get(stop)
        except KeyboardInterrupt:
            stop, error = "interrupted", "已中断。"
        except Exception as exc:
            stop, error = "error", _safe_error(exc, has_images)
        finally:
            if stream is not None:
                try:
                    stream.close()
                except KeyboardInterrupt:
                    stop, error = "interrupted", "已中断。"
                except Exception:
                    # Best-effort cleanup must not mask the stream's outcome.
                    pass
        return SendResult("".join(chunks), usage, stop, error)
