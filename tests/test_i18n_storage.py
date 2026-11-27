import json
import os
import stat
from argparse import Namespace

import pytest

from bedrock_chat import config
from bedrock_chat.config import ConfigError
from bedrock_chat.history import Attachment, HistoryError, HistoryStore, Turn


def test_read_config_can_be_reused_to_load_settings(tmp_path):
    path = tmp_path / "config.toml"
    path.write_text('[ui]\nlanguage = "ja"\n', encoding="utf-8")
    args = Namespace(config=path)
    raw = config.read_config(args)
    path.unlink()

    assert config.configured_language(raw) == "ja"
    configured = config.load_settings(args, raw=raw)
    assert configured.language == "ja" and not configured.language_explicit
    args.lang = "en"
    explicit = config.load_settings(args, raw=raw)
    assert explicit.language == "en" and explicit.language_explicit
    assert config.load_settings(Namespace(), raw={}).language == "zh"


def test_read_config_leaves_schema_validation_to_load_settings(tmp_path):
    path = tmp_path / "config.toml"
    path.write_text('[ui]\nlanguage = "en"\n[unexpected]\nx = 1\n', encoding="utf-8")
    args = Namespace(config=path)
    raw = config.read_config(args)

    assert config.configured_language(raw) == "en"
    with pytest.raises(ConfigError):
        config.load_settings(args, raw=raw)


@pytest.mark.parametrize("ui", [[], {"language": True}, {"language": "fr"}, {"lang": "ja"}])
def test_configured_language_validates_optional_ui_table(ui):
    assert config.configured_language({}) == "zh"
    with pytest.raises(ConfigError):
        config.configured_language({"ui": ui})


def test_language_sidecar_is_private_and_independent_of_last_model(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root)
    assert store.load_language() is None
    assert not root.exists()
    with pytest.raises(ConfigError):
        store.save_language("fr")
    assert not root.exists()

    store.save_last_model("example.model")
    store.save_language("en")
    sidecar = root / "ui-language"
    before = sidecar.stat()
    store.save_language("ja")
    assert HistoryStore(root).load_language() == "ja"
    assert sidecar.read_bytes() == b"ja\n"
    assert sidecar.stat().st_ino != before.st_ino
    assert stat.S_IMODE(sidecar.stat().st_mode) == 0o600
    assert stat.S_IMODE(root.stat().st_mode) == 0o700
    assert store.load_last_model() == "example.model"
    assert sorted(path.name for path in root.iterdir()) == ["last-model", "ui-language"]


def test_disabled_history_never_loads_or_saves_language(tmp_path):
    root = tmp_path / "history"
    disabled = HistoryStore(root, enabled=False)
    disabled.save_language("ja")
    assert not root.exists()
    enabled = HistoryStore(root)
    enabled.save_language("en")
    assert disabled.load_language() is None
    disabled.save_language("ja")
    assert enabled.load_language() == "en"


@pytest.mark.parametrize("data", [b"fr\n", b"en\n\n", b"\xff", b"en\n" + b"x" * 4096])
def test_language_sidecar_ignores_invalid_data(tmp_path, data):
    (tmp_path / "ui-language").write_bytes(data)
    assert HistoryStore(tmp_path).load_language() is None


def test_language_sidecar_does_not_follow_symlinks(tmp_path):
    root = tmp_path / "history"
    root.mkdir()
    target = tmp_path / "outside"
    target.write_bytes(b"ja\n")
    sidecar = root / "ui-language"
    sidecar.symlink_to(target)
    store = HistoryStore(root)
    assert store.load_language() is None
    store.save_language("en")
    assert not sidecar.is_symlink()
    assert store.load_language() == "en"
    assert target.read_bytes() == b"ja\n"

    linked_root = tmp_path / "linked-history"
    linked_root.symlink_to(root, target_is_directory=True)
    linked_store = HistoryStore(linked_root)
    assert linked_store.load_language() is None
    with pytest.raises(HistoryError):
        linked_store.save_language("ja")
    assert store.load_language() == "en"


def test_language_sidecar_ignores_nonregular_files(tmp_path):
    os.mkfifo(tmp_path / "ui-language")
    assert HistoryStore(tmp_path).load_language() is None


@pytest.mark.parametrize(
    ("language", "user_heading", "assistant_heading", "summary", "incomplete", "config_error"),
    [
        ("en", "### User", "### Assistant", "## Summary", "Incomplete: interrupted", "integer"),
        ("ja", "### ユーザー", "### アシスタント", "## 集計", "未完了: interrupted", "整数"),
    ],
)
def test_storage_diagnostics_and_export_use_context_language(
    tmp_path, language, user_heading, assistant_heading, summary, incomplete, config_error
):
    from bedrock_chat.i18n import using_language

    root = tmp_path / "history"
    store = HistoryStore(root)
    with using_language(language):
        # An explicit title matching the Chinese default must not be translated.
        session = store.new("未命名", model_id="example.model")
        turn = Turn(
            "example.model",
            "你好 {name}",
            "こんにちは",
            attachments=[Attachment("截图.png", "png", 2, 3, 4)],
            status="interrupted",
        )
        store.save_turn(session, turn, [])
        loaded = store.load(session.id)
        target = store.export(loaded, tmp_path / "chat.md")
        text = target.read_text(encoding="utf-8")
        with pytest.raises(ConfigError, match=config_error) as raised:
            config.load_settings(Namespace(), raw={"images": {"max_edge": True}})
        assert "images.max_edge" in str(raised.value)
        with pytest.raises(HistoryError) as raised:
            store.load("bad ID")
        assert "会话 ID 或前缀格式无效。" != str(raised.value)

    assert text.splitlines()[0] == "# 未命名"
    assert user_heading in text and assistant_heading in text and summary in text
    assert incomplete in text
    assert "你好 {name}" in text and "こんにちは" in text and "`截图.png`" in text
    state = json.loads((root / session.id / "state.json").read_text(encoding="utf-8"))
    record = json.loads((root / session.id / "turns.jsonl").read_text(encoding="utf-8"))
    assert state["name"] == "未命名"
    assert record["user"] == turn.user and record["assistant"] == turn.assistant
    assert record["status"] == "interrupted" and record["attachments"][0]["name"] == "截图.png"
