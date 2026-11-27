from io import StringIO
from unittest.mock import Mock

import pytest
from rich.console import Console

from bedrock_chat.bedrock import Bedrock
from bedrock_chat.cli import Chat, parse_args
from bedrock_chat.config import ConfigError, load_settings

LOCAL = "http://localhost:11434/v1/chat/completions"


@pytest.fixture(autouse=True)
def isolated_state(tmp_path, monkeypatch):
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "config"))
    monkeypatch.setenv("XDG_STATE_HOME", str(tmp_path / "state"))


def settings(*args, raw=None):
    return load_settings(parse_args(list(args)), raw=raw)


def make_chat(*args, service=None, raw=None):
    return Chat(settings(*args, raw=raw), Console(file=StringIO(), width=120), service)


def test_bedrock_remains_default():
    chat = make_chat("--model", "example.bedrock")
    assert chat.settings.provider == "bedrock"
    assert isinstance(chat.service, Bedrock)
    assert chat.settings.model_id == "example.bedrock"


def test_ollama_defaults_and_model_config():
    raw = {"chat": {"provider": "openai"}, "openai": {"model_id": "qwen2.5:1.5b", "stream": False}}
    configured = settings(raw=raw)
    assert configured.provider == "openai"
    assert configured.endpoint == LOCAL
    assert configured.model_id == "qwen2.5:1.5b"
    assert not configured.openai_stream
    assert settings("--provider", "bedrock", raw=raw).provider == "bedrock"


@pytest.mark.parametrize(
    "endpoint",
    [
        "http://example.com/v1/chat/completions",
        "http://localhost:11434/v1",
        "http://a@localhost:11434/v1/chat/completions",
        "https://example.com/v1/chat/completions?key=hidden",
        "http://localhost:0/v1/chat/completions",
        "http://localhost:65536/v1/chat/completions",
        "https://example.com/a\\b/chat/completions",
    ],
)
def test_bad_endpoints_are_rejected_before_a_request(endpoint):
    with pytest.raises(ConfigError):
        settings("--provider", "openai", "--endpoint", endpoint)


def test_context_and_last_model_are_isolated_by_provider_and_endpoint():
    bedrock = make_chat("--model", "example.bedrock", service=Mock())
    bedrock.dispatch("/model example.private")
    bedrock_session = bedrock.session.id
    local = make_chat("--provider", "openai", "--model", "qwen2.5:1.5b", service=Mock())
    local.dispatch(f"/resume {bedrock_session}")
    assert local.session.id != bedrock_session
    assert local.session.model_id == "qwen2.5:1.5b"
    local.dispatch("/model qwen2.5:3b")
    same = make_chat("--provider", "openai", service=Mock())
    assert same.session.model_id == "qwen2.5:3b"
    other = make_chat(
        "--provider",
        "openai",
        "--endpoint",
        "https://example.com/v1/chat/completions",
        service=Mock(),
    )
    assert other.session.model_id is None
    assert make_chat(service=Mock()).session.model_id == "example.private"
    local.service.send.assert_not_called()


def test_compatible_backend_does_not_inherit_aws_model_from_config():
    cfg = settings("--provider", "openai", raw={"aws": {"model_id": "example.bedrock"}})
    assert cfg.model_id is None
