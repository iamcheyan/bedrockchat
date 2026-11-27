from importlib.metadata import distribution
from io import BytesIO, StringIO
from types import SimpleNamespace

import pytest
from PIL import Image
from prompt_toolkit.document import Document
from prompt_toolkit.input import create_pipe_input
from prompt_toolkit.output import DummyOutput
from rich.console import Console

from bedrock_chat.bedrock import Usage
from bedrock_chat.cli import Chat, create_prompt, main, parse_args
from bedrock_chat.config import load_settings
from bedrock_chat.images import ImageAttachment


def test_termtuck_and_legacy_commands_share_entry_point():
    scripts = {
        entry.name: entry.value
        for entry in distribution("bedrock-chat").entry_points
        if entry.group == "console_scripts"
    }
    assert scripts["termtuck"] == scripts["bedrock-chat"] == "bedrock_chat.cli:main"


class FakeBedrock:
    def __init__(self):
        self.requests = []

    def send(self, request, on_text):
        self.requests.append(request)
        text = "中日 English\n```python\nprint(42)\n```"
        on_text(text[:4])
        on_text(text[4:])
        return SimpleNamespace(
            text=text, usage=Usage(120, 9, 129), stop_reason="end_turn", error=None
        )


def start_chat(monkeypatch, tmp_path, *args):
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "config"))
    monkeypatch.setenv("XDG_STATE_HOME", str(tmp_path / "state"))
    console = Console(file=StringIO(), width=100, force_terminal=False)
    return Chat(load_settings(parse_args(list(args))), console, FakeBedrock())


def make_chat(monkeypatch, tmp_path, *args):
    return start_chat(monkeypatch, tmp_path, "--model", "test.model", *args)


def test_model_selection_survives_restart_without_sending(tmp_path, monkeypatch):
    config = tmp_path / "settings.toml"
    config.write_text('[aws]\nmodel_id = "test.default"\n[models.chosen]\nid = "test.chosen"\n')
    chat = start_chat(monkeypatch, tmp_path, "--config", str(config))
    assert chat.session.model_id == "test.default"
    chat.dispatch("/model chosen")
    chat.dispatch("/model invalid?")

    restarted = start_chat(monkeypatch, tmp_path, "--config", str(config))
    assert restarted.session.model_id == "test.chosen"
    assert restarted.session.turns == []
    assert not chat.service.requests and not restarted.service.requests
    pref = restarted.settings.history_dir / "last-model"
    assert pref.stat().st_mode & 0o777 == 0o600


def test_explicit_startup_model_overrides_saved_model_and_is_remembered_after_use(
    tmp_path, monkeypatch
):
    start_chat(monkeypatch, tmp_path).dispatch("/model test.saved")
    chat = start_chat(monkeypatch, tmp_path, "--model", "test.override")
    assert chat.session.model_id == "test.override"
    assert start_chat(monkeypatch, tmp_path).session.model_id == "test.saved"
    chat.dispatch("hello")
    assert start_chat(monkeypatch, tmp_path).session.model_id == "test.override"


def test_no_history_neither_loads_nor_changes_last_model(tmp_path, monkeypatch):
    start_chat(monkeypatch, tmp_path).dispatch("/model test.saved")
    chat = start_chat(monkeypatch, tmp_path, "--no-history")
    assert chat.session.model_id is None
    chat.dispatch("/model test.private")
    chat.dispatch("hello")
    assert start_chat(monkeypatch, tmp_path).session.model_id == "test.saved"


def test_resume_keeps_the_sessions_own_model(tmp_path, monkeypatch):
    chat = start_chat(monkeypatch, tmp_path)
    chat.dispatch("/model test.older")
    session_id = chat.session.id
    chat.dispatch("/new")
    chat.dispatch("/model test.recent")

    restarted = start_chat(monkeypatch, tmp_path)
    assert restarted.session.model_id == "test.recent"
    restarted.dispatch(f"/resume {session_id}")
    assert restarted.session.model_id == "test.older"
    assert not restarted.service.requests


def test_malformed_last_model_does_not_prevent_startup(tmp_path, monkeypatch):
    chat = start_chat(monkeypatch, tmp_path)
    chat.settings.history_dir.mkdir(parents=True)
    (chat.settings.history_dir / "last-model").write_text("not a model?\n")
    assert start_chat(monkeypatch, tmp_path).session.model_id is None


def test_multimodal_request_explicit_and_history_images_not_resent(tmp_path, monkeypatch):
    chat = make_chat(monkeypatch, tmp_path)
    path = tmp_path / "sample image.png"
    Image.new("RGB", (160, 120), "white").save(path)
    chat.dispatch(f'/image "{path}"')
    chat.dispatch(f'/image "{path}"')
    assert not chat.service.requests
    assert "2 张" in chat.toolbar()
    assert "160×120" in chat.toolbar()
    chat.dispatch("中文\n日本語\nEnglish")
    chat.dispatch("follow up")
    first, second = chat.service.requests
    assert sum("image" in c for c in first["messages"][0]["content"]) == 2
    assert len(second["messages"]) == 3
    assert not any("image" in c for m in second["messages"] for c in m["content"])
    assert "258" in chat.console.file.getvalue()
    saved = chat.store.load(chat.session.id)
    assert len(saved.turns[0].attachments) == 2
    assert saved.turns[0].attachments[0].file is None


def test_local_commands_do_not_send_and_clear_only_changes_context(tmp_path, monkeypatch):
    chat = make_chat(monkeypatch, tmp_path)
    chat.dispatch("hello")
    ident = chat.session.id
    chat.dispatch("/clear")
    chat.dispatch("/new next")
    other = chat.session.id
    chat.dispatch(f"/rename {ident} renamed")
    chat.dispatch("/history hello")
    chat.dispatch(f"/resume {ident}")
    chat.dispatch(f'/export "{tmp_path / "test.md"}"')
    chat.dispatch(f"/delete {other}")
    chat.dispatch("/config")
    chat.dispatch("/tokens")
    chat.dispatch("/help")
    assert len(chat.service.requests) == 1
    assert chat.session.name == "renamed"
    assert len(chat.session.turns) == 1
    assert "hello" in (tmp_path / "test.md").read_text()
    chat.dispatch("again")
    assert len(chat.service.requests[-1]["messages"]) == 1


def test_new_empty_named_session_can_be_resumed(tmp_path, monkeypatch):
    chat = make_chat(monkeypatch, tmp_path)
    chat.dispatch("/new 小さな会話")
    session_id = chat.session.id
    chat.dispatch("/new next")
    chat.dispatch(f"/resume {session_id}")
    assert chat.session.name == "小さな会話"
    assert not chat.service.requests


def test_trailing_newline_keeps_command_like_input_as_chat(tmp_path, monkeypatch):
    chat = make_chat(monkeypatch, tmp_path, "--no-history")
    assert chat.dispatch("/exit\n")
    assert chat.service.requests[0]["messages"][-1]["content"] == [{"text": "/exit\n"}]


def test_saved_attachment_can_be_explicitly_reattached_after_resume(tmp_path, monkeypatch):
    cfg = tmp_path / "settings.toml"
    cfg.write_text("[history]\nsave_images = true\n")
    chat = make_chat(monkeypatch, tmp_path, "--config", str(cfg))
    picture = tmp_path / "test.png"
    Image.new("RGB", (100, 100)).save(picture)
    chat.dispatch(f"/image {picture}")
    chat.dispatch("inspect")
    session_id = chat.session.id
    chat.dispatch("/new")
    chat.dispatch(f"/resume {session_id}")
    chat.dispatch("/image reuse 1 1")
    assert len(chat.images) == len(chat.service.requests) == 1
    chat.dispatch("inspect again")
    assert len(chat.service.requests) == 2
    contents = [block for m in chat.service.requests[-1]["messages"] for block in m["content"]]
    assert sum("image" in block for block in contents) == 1


def test_interrupted_response_is_saved_but_never_resent(tmp_path, monkeypatch):
    chat = make_chat(monkeypatch, tmp_path)
    fake = chat.service
    chat.service = SimpleNamespace(
        send=lambda _request, on_text: SimpleNamespace(
            text="partial",
            usage=None,
            stop_reason="interrupted",
            error="已中断",
        )
    )
    chat.dispatch("first")
    chat.service = fake
    chat.dispatch("second")
    assert len(fake.requests[0]["messages"]) == 1
    turn = chat.store.load(chat.session.id).turns[0]
    assert turn.assistant == "partial" and turn.usage is None and turn.status == "interrupted"
    assert "1 次用量未知" in chat.console.file.getvalue()


def test_text_only_model_never_receives_images(tmp_path, monkeypatch):
    cfg = tmp_path / "settings.toml"
    cfg.write_text('[models.text]\nid = "test.model"\nvision = false\n')
    chat = make_chat(monkeypatch, tmp_path, "--config", str(cfg))
    picture = tmp_path / "test.png"
    Image.new("RGB", (100, 100)).save(picture)
    chat.dispatch(f"/image {picture}")
    assert not chat.images and not chat.service.requests
    assert "不支持图片" in chat.console.file.getvalue()


def test_keyboard_pastes_only_on_ctrl_g_without_discarding_multiline_draft(tmp_path, monkeypatch):
    chat = make_chat(monkeypatch, tmp_path)
    png = BytesIO()
    Image.new("RGB", (100, 100)).save(png, format="PNG")
    called = []

    def paste(**kwargs):
        called.append(kwargs)
        return ImageAttachment(
            data=png.getvalue(), format="png", width=100, height=100, name="clip"
        )

    monkeypatch.setattr("bedrock_chat.cli.paste_image", paste)
    with create_pipe_input() as pipe:
        prompt = create_prompt(chat, input=pipe, output=DummyOutput())
        pipe.send_text("你好\x1b\r日本語\x07English\r")
        chat.dispatch(prompt.prompt())
    assert chat.service.requests[0]["messages"][-1]["content"][-1] == {
        "text": "你好\n日本語English"
    }
    assert chat.session.turns[-1].user == "你好\n日本語English"
    assert len(called) == 1
    assert not chat.images


@pytest.mark.parametrize("key", ["\x7f", "\x08", "\x1b[D\x1b[3~"])
def test_inline_image_can_be_removed_in_mixed_draft(tmp_path, monkeypatch, key):
    chat = make_chat(monkeypatch, tmp_path)
    attachments = iter(
        [
            ImageAttachment(b"first image", "png", 16, 12, "first.png"),
            ImageAttachment(b"second image", "png", 20, 17, "second.png"),
        ]
    )
    monkeypatch.setattr("bedrock_chat.cli.paste_image", lambda **_: next(attachments))
    with create_pipe_input() as pipe:
        prompt = create_prompt(chat, input=pipe, output=DummyOutput())
        pipe.send_text(f"你好\x07 middle \x07{key}finished\r")
        chat.dispatch(prompt.prompt())
    blocks = chat.service.requests[0]["messages"][-1]["content"]
    assert [block["image"]["source"]["bytes"] for block in blocks if "image" in block] == [
        b"first image"
    ]
    assert blocks[-1] == {"text": "你好 middle finished"}
    assert [i.name for i in chat.session.turns[0].attachments] == ["first.png"]
    assert prompt.history.get_strings() == ["你好 middle finished"]


@pytest.mark.parametrize("backspace", ["\x7f", "\x08"])
def test_backspace_removes_one_image_at_a_time_from_image_only_draft(
    tmp_path, monkeypatch, backspace
):
    chat = make_chat(monkeypatch, tmp_path)
    images = [
        ImageAttachment(data=b"one", format="png", width=10, height=20, name="one.png"),
        ImageAttachment(data=b"two", format="png", width=30, height=40, name="two.png"),
    ]
    monkeypatch.setattr("bedrock_chat.cli.paste_image", lambda **_: images.pop(0))
    with create_pipe_input() as pipe:
        prompt = create_prompt(chat, input=pipe, output=DummyOutput())
        pipe.send_text(f"\x07\x07{backspace}x{backspace}\r")
        draft = prompt.prompt()
    assert len(draft) == 1
    assert [image.name for image in chat.images] == ["one.png"]

    with create_pipe_input() as pipe:
        prompt = create_prompt(chat, input=pipe, output=DummyOutput())
        pipe.send_text(f"{backspace}{backspace}\r")
        chat.dispatch(prompt.prompt(default=Document(draft, len(draft))))
    assert not chat.images and not chat.service.requests


def test_paste_at_cursor_preserves_text_on_both_sides(tmp_path, monkeypatch):
    chat = make_chat(monkeypatch, tmp_path)
    monkeypatch.setattr(
        "bedrock_chat.cli.paste_image", lambda **_: ImageAttachment(b"image", "png", 10, 10, "clip")
    )
    with create_pipe_input() as pipe:
        prompt = create_prompt(chat, input=pipe, output=DummyOutput())
        pipe.send_text("左尾\x1b[D\x07右\r")
        chat.dispatch(prompt.prompt())
    blocks = chat.service.requests[0]["messages"][-1]["content"]
    assert blocks[-1] == {"text": "左右尾"}
    assert blocks[0]["image"]["source"]["bytes"] == b"image"


def test_image_commands_keep_remaining_chips_in_next_prompt(tmp_path, monkeypatch):
    chat = make_chat(monkeypatch, tmp_path)
    for name in ("one.png", "two.png"):
        path = tmp_path / name
        Image.new("RGB", (10, 10)).save(path)
        chat.dispatch(f"/image {path}")
    with create_pipe_input() as pipe:
        prompt = create_prompt(chat, input=pipe, output=DummyOutput())
        pipe.send_text("/image drop 1\r")
        chat.dispatch(prompt.prompt(default=chat.draft))
        assert [image.name for image in chat.images] == ["two.png"]
        assert not chat.service.requests
        pipe.send_text("\x7f\r")
        chat.dispatch(prompt.prompt(default=chat.draft))
    assert not chat.images and not chat.service.requests


def test_literal_image_label_is_sent_as_text(tmp_path, monkeypatch):
    chat = make_chat(monkeypatch, tmp_path)
    with create_pipe_input() as pipe:
        prompt = create_prompt(chat, input=pipe, output=DummyOutput())
        pipe.send_text("[图片 #1]\r")
        chat.dispatch(prompt.prompt())
    assert chat.service.requests[0]["messages"][-1]["content"] == [{"text": "[图片 #1]"}]


def test_bracketed_paste_cannot_trigger_image_read_or_send(tmp_path, monkeypatch):
    chat = make_chat(monkeypatch, tmp_path)

    def unexpected(**kwargs):
        raise AssertionError("合并的文本粘贴不得读取图片")

    monkeypatch.setattr("bedrock_chat.cli.paste_image", unexpected)
    with create_pipe_input() as pipe:
        prompt = create_prompt(chat, input=pipe, output=DummyOutput())
        pipe.send_text("\x1b[200~line1\nline2\x07\x1b[201~\r")
        assert prompt.prompt() == "line1\nline2"
    assert not chat.images and not chat.service.requests


def test_main_quick_clipboard_diagnostic_is_offline(tmp_path, monkeypatch, capsys):
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    monkeypatch.setenv("XDG_STATE_HOME", str(tmp_path / "state"))
    monkeypatch.setattr(
        "bedrock_chat.cli.paste_image",
        lambda **_: SimpleNamespace(
            width=800,
            height=600,
            format="png",
            data=b"1234",
            name="clip",
        ),
    )
    assert main(["--check-clipboard"]) == 0
    assert "800×600" in capsys.readouterr().out
    assert not (tmp_path / "state").exists()
