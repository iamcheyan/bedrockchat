"""Local settings; this module never loads AWS credentials."""

import os
import re
import tomllib
from dataclasses import dataclass
from decimal import Decimal
from hashlib import sha256
from pathlib import Path
from urllib.parse import urlsplit, urlunsplit

from .i18n import LANGUAGES, tr

PROVIDERS = ("bedrock", "openai")
OLLAMA_ENDPOINT = "http://localhost:11434/v1/chat/completions"


class ConfigError(ValueError):
    pass


def terminal_text(text: str) -> str:
    """Strip terminal control characters before displaying untrusted content."""
    return re.sub(r"[\x00-\x08\x0b-\x1f\x7f-\x9f]", "", text.replace("\r\n", "\n"))


def xdg_path(variable: str, fallback: str) -> Path:
    path = Path(os.environ.get(variable, ""))
    return (path if path.is_absolute() else Path.home() / fallback) / "bedrock-chat"


@dataclass(frozen=True)
class ModelInfo:
    id: str
    vision: bool | None = None
    input_price: Decimal | None = None
    output_price: Decimal | None = None

    def cost(self, input_tokens: int, output_tokens: int) -> Decimal | None:
        if self.input_price is None or self.output_price is None:
            return None
        return (self.input_price * input_tokens + self.output_price * output_tokens) / 1_000_000


@dataclass(frozen=True)
class Settings:
    region: str | None
    profile: str | None
    model_id: str | None
    max_input_tokens: int
    max_output_tokens: int
    system_prompt: str
    history_enabled: bool
    save_images: bool
    history_dir: Path
    max_edge: int
    max_bytes: int
    models: dict[str, ModelInfo]
    model_explicit: bool = False
    language: str = "zh"
    language_explicit: bool = False
    provider: str = "bedrock"
    endpoint: str = OLLAMA_ENDPOINT
    openai_stream: bool = True

    @property
    def conversation_dir(self) -> Path:
        if self.provider == "bedrock":
            return self.history_dir
        scope = sha256(self.endpoint.encode()).hexdigest()[:24]
        return self.history_dir.with_name(f"{self.history_dir.name}-openai-{scope}")

    def resolve_model(self, choice: str) -> str:
        return self.models[choice].id if choice in self.models else validate_model(choice)

    def model_info(self, model_id: str) -> ModelInfo:
        return next(
            (model for model in self.models.values() if model.id == model_id), ModelInfo(model_id)
        )


def validate_model(value: str) -> str:
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9.:_/-]{0,2047}", value):
        raise ConfigError(tr("模型 ID 或 ARN 格式无效。"))
    if re.search(r":prompt(?:-version)?/", value):
        raise ConfigError(tr("不支持 Prompt Management ARN；请使用模型 ID 或推理配置文件 ID/ARN。"))
    return value


def validate_endpoint(value: str) -> str:
    error = tr(
        "兼容端点必须是完整的 /chat/completions URL；仅回环地址允许 HTTP，不得包含凭证、查询或片段。"
    )
    if not re.fullmatch(r"[\x21-\x7e]+", value) or any(c in value for c in "\\?#"):
        raise ConfigError(error)
    try:
        parsed = urlsplit(value)
        port = parsed.port
        host = parsed.hostname
    except ValueError:
        raise ConfigError(error) from None
    if (
        not host
        or parsed.username is not None
        or parsed.password is not None
        or port == 0
        or not parsed.path.endswith("/chat/completions")
        or not re.fullmatch(
            r"\[[0-9A-Fa-f:]+\](?::[0-9]+)?|[A-Za-z0-9.-]+(?::[0-9]+)?", parsed.netloc
        )
        or not (
            parsed.scheme == "https"
            or (parsed.scheme == "http" and host in {"localhost", "127.0.0.1", "::1"})
        )
    ):
        raise ConfigError(error)
    # Canonicalize scheme/host/default port to keep the state namespace stable.
    netloc = f"[{host}]" if ":" in host else host
    if port is not None and port != (443 if parsed.scheme == "https" else 80):
        netloc += f":{port}"
    return urlunsplit((parsed.scheme, netloc, parsed.path, "", ""))


def validate_language(value: object) -> str:
    if not isinstance(value, str) or value not in LANGUAGES:
        raise ConfigError(tr("界面语言必须是 zh、en 或 ja。"))
    return value


def _table(value: object, allowed: set[str] | None, label: str) -> dict:
    if not isinstance(value, dict) or (allowed is not None and (value.keys() - allowed)):
        raise ConfigError(
            tr("{label} 配置格式有误或含有未知字段。不要在配置中填写凭证。", label=label)
        )
    return value


def _string(value: object, label: str, *, optional: bool = False) -> str | None:
    if value is None and optional:
        return None
    if not isinstance(value, str) or "\x00" in value or (optional and not value.strip()):
        raise ConfigError(tr("{label} 必须是有效的字符串。", label=label))
    return value


def _boolean(value: object, label: str) -> bool:
    if type(value) is not bool:
        raise ConfigError(tr("{label} 必须是 true 或 false。", label=label))
    return value


def _integer(value: object, low: int, high: int, label: str) -> int:
    if type(value) is not int or not low <= value <= high:
        raise ConfigError(
            tr("{label} 必须是 {low}～{high} 内的整数。", label=label, low=low, high=high)
        )
    return value


def _price(value: object) -> Decimal:
    if type(value) not in (int, float, Decimal):
        raise ConfigError(tr("模型价格必须是非负数字。"))
    number = Decimal(str(value))
    if not number.is_finite() or not 0 <= number <= 1_000_000:
        raise ConfigError(tr("模型价格必须是 0～1000000 内的有限数字。"))
    return number


def read_config(args) -> dict:
    """Read TOML from the selected path without validating the settings schema."""
    explicit_path = getattr(args, "config", None)
    path = (
        Path(explicit_path).expanduser()
        if explicit_path
        else (xdg_path("XDG_CONFIG_HOME", ".config") / "config.toml")
    )
    try:
        with path.open("rb") as file:
            return tomllib.load(file, parse_float=Decimal)
    except FileNotFoundError:
        if explicit_path:
            raise ConfigError(tr("找不到指定的配置文件。")) from None
        return {}
    except (OSError, UnicodeError, ValueError):
        raise ConfigError(tr("无法读取配置文件，请检查文件权限与 TOML 语法。")) from None


def configured_language(raw: dict) -> str:
    """Validate the optional UI table independently of the rest of the settings."""
    ui = _table(raw.get("ui", {}), {"language"}, "[ui]")
    return validate_language(ui.get("language", "zh"))


def load_settings(args, *, raw: dict | None = None) -> Settings:
    if raw is None:
        raw = read_config(args)
    _table(raw, {"aws", "openai", "chat", "history", "images", "models", "ui"}, tr("顶层"))
    language = configured_language(raw)
    explicit_language = getattr(args, "lang", None)
    if explicit_language is not None:
        language = validate_language(explicit_language)
    aws = _table(raw.get("aws", {}), {"region", "profile", "model_id"}, "[aws]")
    compatible = _table(raw.get("openai", {}), {"endpoint", "model_id", "stream"}, "[openai]")
    chat = _table(
        raw.get("chat", {}),
        {"provider", "max_input_tokens", "max_output_tokens", "system_prompt"},
        "[chat]",
    )
    history = _table(raw.get("history", {}), {"enabled", "save_images"}, "[history]")
    images = _table(raw.get("images", {}), {"max_edge", "max_bytes"}, "[images]")
    models = {}
    seen_ids = set()
    for alias, item in _table(raw.get("models", {}), None, "[models]").items():
        if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}", alias):
            raise ConfigError(tr("模型别名必须为 1～64 个英文字母、数字、点、下划线或连字符。"))
        _table(item, {"id", "vision", "input_per_million", "output_per_million"}, tr("模型"))
        model_id = validate_model(_string(item.get("id"), tr("模型 ID")))
        if model_id in seen_ids:
            raise ConfigError(tr("不同模型别名不能使用同一个模型 ID。"))
        seen_ids.add(model_id)
        vision = _boolean(item["vision"], "vision") if "vision" in item else None
        has_input, has_output = "input_per_million" in item, "output_per_million" in item
        if has_input != has_output:
            raise ConfigError(tr("输入与输出价格需同时填写。"))
        models[alias] = ModelInfo(
            model_id,
            vision,
            _price(item["input_per_million"]) if has_input else None,
            _price(item["output_per_million"]) if has_output else None,
        )

    def override(key: str, table: dict, default=None):
        value = getattr(args, key, None)
        return table.get(key, default) if value is None else value

    provider = override("provider", chat, "bedrock")
    if provider not in PROVIDERS:
        raise ConfigError(tr("后端必须是 bedrock 或 openai。"))
    if provider != "openai" and getattr(args, "endpoint", None) is not None:
        raise ConfigError(tr("--endpoint 仅可用于 --provider openai。"))
    endpoint = validate_endpoint(
        _string(override("endpoint", compatible, OLLAMA_ENDPOINT), "endpoint")
    )
    stream = _boolean(compatible.get("stream", True), "openai.stream")
    choice = _string(
        override("model_id", compatible if provider == "openai" else aws), "model_id", optional=True
    )
    model_id = models[choice].id if choice in models else validate_model(choice) if choice else None
    region = _string(override("region", aws), "region", optional=True)
    if region is not None and not re.fullmatch(r"[a-z]{2}(?:-[a-z0-9]+)+-\d+", region):
        raise ConfigError(tr("AWS Region 格式无效。"))
    profile = _string(override("profile", aws), "profile", optional=True)
    enabled = _boolean(history.get("enabled", True), "history.enabled")
    save_images = _boolean(history.get("save_images", False), "history.save_images")
    return Settings(
        region,
        profile,
        model_id,
        _integer(override("max_input_tokens", chat, 6000), 64, 4_000_000, "max_input_tokens"),
        _integer(override("max_output_tokens", chat, 1024), 1, 256_000, "max_output_tokens"),
        _string(chat.get("system_prompt", ""), "system_prompt"),
        enabled and not getattr(args, "no_history", False),
        enabled
        and save_images
        and not (getattr(args, "no_history", False) or getattr(args, "no_save_images", False)),
        xdg_path("XDG_STATE_HOME", ".local/state"),
        _integer(images.get("max_edge", 1568), 64, 8000, "images.max_edge"),
        _integer(images.get("max_bytes", 1_500_000), 1024, 3_750_000, "images.max_bytes"),
        models,
        model_explicit=getattr(args, "model_id", None) is not None,
        language=language,
        language_explicit=explicit_language is not None,
        provider=provider,
        endpoint=endpoint,
        openai_stream=stream,
    )
