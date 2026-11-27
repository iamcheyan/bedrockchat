"""Local settings; this module never loads AWS credentials."""

import os
import re
import tomllib
from dataclasses import dataclass
from decimal import Decimal
from pathlib import Path


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

    def resolve_model(self, choice: str) -> str:
        return self.models[choice].id if choice in self.models else validate_model(choice)

    def model_info(self, model_id: str) -> ModelInfo:
        return next(
            (model for model in self.models.values() if model.id == model_id), ModelInfo(model_id)
        )


def validate_model(value: str) -> str:
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9.:_/-]{0,2047}", value):
        raise ConfigError("模型 ID 或 ARN 格式无效。")
    if re.search(r":prompt(?:-version)?/", value):
        raise ConfigError("不支持 Prompt Management ARN；请使用模型 ID 或推理配置文件 ID/ARN。")
    return value


def _table(value: object, allowed: set[str] | None, label: str) -> dict:
    if not isinstance(value, dict) or (allowed is not None and (value.keys() - allowed)):
        raise ConfigError(f"{label} 配置格式有误或含有未知字段。不要在配置中填写凭证。")
    return value


def _string(value: object, label: str, *, optional: bool = False) -> str | None:
    if value is None and optional:
        return None
    if not isinstance(value, str) or "\x00" in value or (optional and not value.strip()):
        raise ConfigError(f"{label} 必须是有效的字符串。")
    return value


def _boolean(value: object, label: str) -> bool:
    if type(value) is not bool:
        raise ConfigError(f"{label} 必须是 true 或 false。")
    return value


def _integer(value: object, low: int, high: int, label: str) -> int:
    if type(value) is not int or not low <= value <= high:
        raise ConfigError(f"{label} 必须是 {low}～{high} 内的整数。")
    return value


def _price(value: object) -> Decimal:
    if type(value) not in (int, float, Decimal):
        raise ConfigError("模型价格必须是非负数字。")
    number = Decimal(str(value))
    if not number.is_finite() or not 0 <= number <= 1_000_000:
        raise ConfigError("模型价格必须是 0～1000000 内的有限数字。")
    return number


def load_settings(args) -> Settings:
    explicit_path = getattr(args, "config", None)
    path = (
        Path(explicit_path).expanduser()
        if explicit_path
        else (xdg_path("XDG_CONFIG_HOME", ".config") / "config.toml")
    )
    try:
        with path.open("rb") as file:
            raw = tomllib.load(file, parse_float=Decimal)
    except FileNotFoundError:
        if explicit_path:
            raise ConfigError("找不到指定的配置文件。") from None
        raw = {}
    except (OSError, UnicodeError, ValueError):
        raise ConfigError("无法读取配置文件，请检查文件权限与 TOML 语法。") from None
    _table(raw, {"aws", "chat", "history", "images", "models"}, "顶层")
    aws = _table(raw.get("aws", {}), {"region", "profile", "model_id"}, "[aws]")
    chat = _table(
        raw.get("chat", {}), {"max_input_tokens", "max_output_tokens", "system_prompt"}, "[chat]"
    )
    history = _table(raw.get("history", {}), {"enabled", "save_images"}, "[history]")
    images = _table(raw.get("images", {}), {"max_edge", "max_bytes"}, "[images]")
    models = {}
    seen_ids = set()
    for alias, item in _table(raw.get("models", {}), None, "[models]").items():
        if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}", alias):
            raise ConfigError("模型别名必须为 1～64 个英文字母、数字、点、下划线或连字符。")
        _table(item, {"id", "vision", "input_per_million", "output_per_million"}, "模型")
        model_id = validate_model(_string(item.get("id"), "模型 ID"))
        if model_id in seen_ids:
            raise ConfigError("不同模型别名不能使用同一个模型 ID。")
        seen_ids.add(model_id)
        vision = _boolean(item["vision"], "vision") if "vision" in item else None
        has_input, has_output = "input_per_million" in item, "output_per_million" in item
        if has_input != has_output:
            raise ConfigError("输入与输出价格需同时填写。")
        models[alias] = ModelInfo(
            model_id,
            vision,
            _price(item["input_per_million"]) if has_input else None,
            _price(item["output_per_million"]) if has_output else None,
        )

    def override(key: str, table: dict, default=None):
        value = getattr(args, key, None)
        return table.get(key, default) if value is None else value

    choice = _string(override("model_id", aws), "model_id", optional=True)
    model_id = models[choice].id if choice in models else validate_model(choice) if choice else None
    region = _string(override("region", aws), "region", optional=True)
    if region is not None and not re.fullmatch(r"[a-z]{2}(?:-[a-z0-9]+)+-\d+", region):
        raise ConfigError("AWS Region 格式无效。")
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
    )
