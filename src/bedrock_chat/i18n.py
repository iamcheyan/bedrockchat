"""Offline interface translations scoped to the active chat or command."""

from collections.abc import Iterator
from contextlib import contextmanager
from contextvars import ContextVar
from functools import cache

DEFAULT_LANGUAGE = "zh"
LANGUAGES = {"zh": "简体中文", "en": "English", "ja": "日本語"}
_language = ContextVar("bedrockchat_language", default=DEFAULT_LANGUAGE)


@contextmanager
def using_language(language: str) -> Iterator[None]:
    token = _language.set(language)
    try:
        yield
    finally:
        _language.reset(token)


@cache
def _catalog() -> dict[str, tuple[str, str]]:
    from ._messages_backend import MESSAGES as backend
    from ._messages_cli import MESSAGES as cli
    from ._messages_openai import MESSAGES as openai
    from ._messages_storage import MESSAGES as storage

    return {**backend, **storage, **cli, **openai}


def tr(message: str, **values: object) -> str:
    """Translate an application string; inserted values are never translated."""
    language = _language.get()
    if language != DEFAULT_LANGUAGE:
        translations = _catalog().get(message)
        if translations is not None:
            message = translations[0 if language == "en" else 1]
    return message.format(**values) if values else message
