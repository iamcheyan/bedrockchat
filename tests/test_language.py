"""Language settings must change the interface, not the AWS request."""

from io import StringIO
from types import SimpleNamespace
from unittest.mock import Mock

import pytest
from rich.console import Console

from bedrock_chat.bedrock import Usage
from bedrock_chat.cli import Chat, main, parse_args
from bedrock_chat.config import load_settings


@pytest.fixture(autouse=True)
def isolated_paths(tmp_path, monkeypatch):
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "config"))
    monkeypatch.setenv("XDG_STATE_HOME", str(tmp_path / "state"))


def make_chat(*args):
    console = Console(file=StringIO(), width=120, force_terminal=False)
    service = Mock()
    service.send.return_value = SimpleNamespace(
        text="unchanged reply", usage=Usage(5, 6, 11), stop_reason="end_turn", error=None
    )
    return Chat(load_settings(parse_args(list(args))), console, service)


@pytest.mark.parametrize(
    "language,help_text,toolbar",
    [
        ("zh", "创建会话", "图片 0 张"),
        ("en", "Create a session", "Images: 0"),
        ("ja", "セッションを作成", "画像 0 枚"),
    ],
)
def test_language_localizes_help_and_toolbar(language, help_text, toolbar):
    chat = make_chat("--lang", language)
    chat.dispatch("/help")
    assert help_text in chat.console.file.getvalue()
    assert toolbar in chat.toolbar()
    chat.service.send.assert_not_called()


def test_switch_takes_effect_immediately_and_survives_restart(tmp_path):
    config = tmp_path / "config.toml"
    config.write_text('[ui]\nlanguage = "en"\n')
    chat = make_chat("--config", str(config))
    assert chat.language == "en"
    chat.dispatch("/lang ja")
    assert "日本語" in chat.console.file.getvalue()
    assert "画像 0 枚" in chat.toolbar()
    assert make_chat("--config", str(config)).language == "ja"
    assert make_chat("--config", str(config), "--lang", "zh").language == "zh"
    assert make_chat("--config", str(config), "--no-history").language == "en"
    chat.service.send.assert_not_called()


def test_translations_do_not_change_model_prompt_or_messages():
    requests = []
    for language in ("zh", "en", "ja"):
        chat = make_chat("--lang", language, "--no-history", "--model", "example.model")
        chat.dispatch("日本語 and 中文")
        requests.append(chat.service.send.call_args.args[0])
        assert "unchanged reply" in chat.console.file.getvalue()
    assert all(request == requests[0] for request in requests)
    assert requests[0]["messages"][0]["content"] == [{"text": "日本語 and 中文"}]
    assert "system" not in requests[0]


def test_help_uses_saved_language_and_explicit_override(capsys):
    chat = make_chat()
    chat.dispatch("/lang ja")
    with pytest.raises(SystemExit) as saved:
        main(["--help"])
    assert saved.value.code == 0
    assert "クリップボード" in capsys.readouterr().out
    with pytest.raises(SystemExit) as override:
        main(["--lang=en", "--help"])
    assert override.value.code == 0
    assert "clipboard" in capsys.readouterr().out
