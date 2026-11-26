import json
import os
import stat
import threading
from datetime import datetime, timezone
from pathlib import Path

import pytest

from bedrock_chat.history import (
    Attachment,
    HistoryError,
    HistoryStore,
    Session,
    Turn,
    default_root,
)

MODEL = "anthropic.example-model"


def records(root: Path, session_id: str) -> list[dict]:
    path = root / session_id / "turns.jsonl"
    if not path.exists():
        return []
    return [
        json.loads(line) for line in path.read_text(encoding="utf-8").split("\n") if line.strip()
    ]


def modes(path: Path) -> int:
    return stat.S_IMODE(path.stat().st_mode)


def turn(user: str, assistant: str, **kwargs) -> Turn:
    return Turn(model_id=MODEL, user=user, assistant=assistant, **kwargs)


# --------------------------------------------------------------------- creation


def test_new_session_defaults_and_is_purely_in_memory(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root)
    session = store.new()
    assert session.name == "未命名"
    assert session.model_id is None
    assert session.context_start == 0 and session.turns == []
    parsed = datetime.fromisoformat(session.created_at)
    assert parsed.tzinfo == timezone.utc
    assert len(session.id) == 32 and int(session.id, 16) >= 0
    assert not root.exists()


def test_default_root_uses_xdg_state_home(tmp_path, monkeypatch):
    monkeypatch.setenv("XDG_STATE_HOME", str(tmp_path / "state"))
    assert default_root() == tmp_path / "state" / "bedrock-chat"
    monkeypatch.setenv("XDG_STATE_HOME", "")
    assert default_root() == Path.home() / ".local" / "state" / "bedrock-chat"


# ---------------------------------------------------------------------- writing


def test_roundtrip_multilingual_turns(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root)
    session = store.new("中文 セッション English", model_id=MODEL)
    first = turn(
        "你好，帮我看看这个错误。",
        "こんにちは。まずログを確認しましょう。",
        usage={"input_tokens": 12, "output_tokens": 34, "total_tokens": 46},
    )
    second = turn("Thanks!\n第二行", "You are welcome. どういたしまして。")
    store.save_turn(session, first, [])
    store.save_turn(session, second, [])

    assert len(session.turns) == 2
    loaded = store.load(session.id)
    assert loaded.id == session.id
    assert loaded.name == "中文 セッション English"
    assert [item.user for item in loaded.turns] == [first.user, second.user]
    assert loaded.turns[0].assistant == first.assistant
    assert loaded.turns[0].usage == {"input_tokens": 12, "output_tokens": 34, "total_tokens": 46}
    assert loaded.turns[1].usage is None
    assert loaded.created_at == session.created_at
    assert loaded.model_id == MODEL
    # session.model_id is not inferred from turns
    assert store.load(session.id[:10]).turns == loaded.turns


def test_save_turn_is_idempotent_for_the_same_turn(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root)
    session = store.new()
    item = turn("只保存一次", "exactly once")
    store.save_turn(session, item, [])
    store.save_turn(session, item, [])
    store.save_turn(session, item, [])
    assert len(records(root, session.id)) == 1
    assert len(session.turns) == 1


def test_failed_save_does_not_mutate_session_or_write_a_record(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root, save_images=True)
    session = store.new()
    item = turn("带图", "image", attachments=[Attachment("a.png", "png", 1, 1, 4)])
    with pytest.raises(HistoryError):
        store.save_turn(session, item, [])  # one attachment, zero payloads
    assert session.turns == []
    assert records(root, session.id) == []
    assert not (root / session.id / "attachments").exists()


def test_save_state_persists_title_context_and_model(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root)
    session = store.new("旧标题")
    store.save_turn(session, turn("a", "b"), [])
    store.save_state(session)
    session.name = "新标题"
    session.context_start = 3
    session.model_id = "other-model.1"
    store.save_state(session)

    loaded = store.load(session.id)
    assert (loaded.name, loaded.context_start, loaded.model_id) == ("新标题", 3, "other-model.1")
    # the state file really carries the new title, not the one from the first turn
    state = json.loads((root / session.id / "state.json").read_text(encoding="utf-8"))
    assert state["name"] == "新标题" and state["context_start"] == 3
    assert state["created_at"] == session.created_at
    datetime.fromisoformat(state["updated_at"])


def test_state_file_is_replaced_atomically_without_temp_leftovers(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root)
    session = store.new()
    for number in range(5):
        store.save_turn(session, turn(str(number), "reply"), [])
        store.save_state(session)
    leftovers = [path.name for path in (root / session.id).iterdir() if ".tmp" in path.name]
    assert leftovers == []
    json.loads((root / session.id / "state.json").read_text(encoding="utf-8"))


def test_context_start_and_model_switch_survive_reload(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root)
    session = store.new("清空后")
    store.save_turn(session, turn("上下文", "次の文脈から"), [])
    session.context_start = 1  # /clear: transcript kept, model context starts later
    session.model_id = "switched-model"
    store.save_state(session)

    listed = store.list()
    assert [item.id for item in listed] == [session.id]
    assert listed[0].turns == [] and listed[0].context_start == 1
    assert listed[0].model_id == "switched-model"
    loaded = store.load(session.id)
    assert loaded.context_start == 1
    assert [item.user for item in loaded.turns] == ["上下文"]


def test_rename_persists_and_reloads(tmp_path):
    store = HistoryStore(tmp_path / "history")
    session = store.new("before")
    store.save_turn(session, turn("a", "b"), [])
    renamed = store.rename(session.id[:6], "リネーム後")
    assert renamed.name == "リネーム後"
    assert store.load(session.id).name == "リネーム後"
    with pytest.raises(HistoryError):
        store.rename(session.id, "")
    with pytest.raises(HistoryError):
        store.rename(session.id, "bad\x00name")


# ------------------------------------------------------------------ attachments


def test_attachment_metadata_without_saving_images(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root, save_images=False)
    session = store.new()
    item = turn(
        "看图",
        "已阅读",
        attachments=[Attachment("截图.png", "png", 100, 50, 1234)],
    )
    store.save_turn(session, item, [b"payload that is intentionally ignored"])
    assert session.turns[0].attachments[0].file is None
    assert (root / session.id / "attachments").exists() is False
    stored = records(root, session.id)[0]
    assert stored["timestamp"] and stored["saved_at"]
    assert stored["attachments"] == [
        {
            "name": "截图.png",
            "format": "png",
            "width": 100,
            "height": 50,
            "size": 1234,
            "file": None,
        }
    ]
    loaded = store.load(session.id)
    assert loaded.turns[0].attachments[0].name == "截图.png"
    with pytest.raises(HistoryError, match="save_images"):
        store.attachment_path(loaded, 1, 1)


def test_saved_images_are_relative_one_based_and_private(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root, save_images=True)
    session = store.new()
    first = turn(
        "图一",
        "ok",
        attachments=[
            Attachment("a.png", "PNG", 2, 3, 5),
            Attachment("b.jpg", "jpeg", 4, 5, 6),
        ],
    )
    second = turn("图二", "ok", attachments=[Attachment("c.png", "png", 1, 1, 3)])
    store.save_turn(session, first, [b"aaa", b"bbb"])
    store.save_turn(session, second, [b"ccc"])

    assert session.turns[0].attachments[0].file == "attachments/0001-01.png"
    assert session.turns[0].attachments[1].file == "attachments/0001-02.jpeg"
    assert session.turns[1].attachments[0].file == "attachments/0002-01.png"
    stored = records(root, session.id)
    assert stored[0]["attachments"][0]["file"] == "attachments/0001-01.png"
    assert stored[0]["attachments"][1]["file"] == "attachments/0001-02.jpeg"
    assert stored[1]["attachments"][0]["file"] == "attachments/0002-01.png"
    reloaded = store.load(session.id)
    assert [[attachment.file for attachment in item.attachments] for item in reloaded.turns] == [
        ["attachments/0001-01.png", "attachments/0001-02.jpeg"],
        ["attachments/0002-01.png"],
    ]

    path = store.attachment_path(session, 1, 2)
    assert path == root / session.id / "attachments" / "0001-02.jpeg"
    assert path.read_bytes() == b"bbb"
    assert store.attachment_path(session, 2, 1).read_bytes() == b"ccc"
    assert modes(path) == 0o600
    assert modes(root) == 0o700
    assert modes(root / session.id) == 0o700
    assert modes(root / session.id / "attachments") == 0o700
    assert modes(root / session.id / "turns.jsonl") == 0o600
    assert modes(root / session.id / "state.json") == 0o600
    assert modes(root / session.id / ".lock") == 0o600

    for bad_turn, bad_image in ((0, 1), (3, 1), (1, 0), (1, 3)):
        with pytest.raises(HistoryError):
            store.attachment_path(session, bad_turn, bad_image)


def test_pre_existing_loose_permissions_are_hardened(tmp_path):
    root = tmp_path / "history"
    root.mkdir(mode=0o755)
    os.chmod(root, 0o755)
    store = HistoryStore(root)
    session = store.new()
    store.save_turn(session, turn("a", "b"), [])
    assert modes(root) == 0o700
    assert modes(root / session.id) == 0o700


# ------------------------------------------------------------------- listing


def test_list_searches_names_and_contents_and_orders_by_update(tmp_path):
    store = HistoryStore(tmp_path / "history")
    japanese = store.new("日本語のタイトル")
    store.save_turn(japanese, turn("最初", "ありがとう"), [])
    english = store.new("English title")
    store.save_turn(english, turn("Hello there", "general kenobi"), [])
    chinese = store.new("中文标题")
    store.save_turn(
        chinese,
        turn("附件测试", "完成了", attachments=[Attachment("图表.png", "png", 1, 1, 1)]),
        [],
    )

    assert [item.id for item in store.list()] == [chinese.id, english.id, japanese.id]
    assert store.list()[0].turns == []  # turns are omitted for an empty query

    assert [item.id for item in store.list("kenobi")] == [english.id]
    assert store.list("kenobi")[0].turns[0].assistant == "general kenobi"
    assert [item.id for item in store.list("タイトル")] == [japanese.id]
    assert [item.id for item in store.list("图表")] == [chinese.id]
    assert store.list("nothing matches") == []

    # touching an older session moves it to the front
    store.save_turn(japanese, turn("追記", "どうぞ"), [])
    assert [item.id for item in store.list()] == [japanese.id, chinese.id, english.id]


def test_list_on_empty_or_missing_root(tmp_path):
    store = HistoryStore(tmp_path / "missing")
    assert store.list() == []
    assert not (tmp_path / "missing").exists()
    with pytest.raises(HistoryError):
        store.load("a" * 32)


# --------------------------------------------------------------------- lookup


def test_prefix_lookup_ambiguity(tmp_path):
    store = HistoryStore(tmp_path / "history")
    one = Session(id="abc111" + "0" * 26)
    two = Session(id="abc222" + "0" * 26)
    for session in (one, two):
        store.save_turn(session, turn(session.id[:3], "x"), [])
    assert store.load("abc1").id == one.id
    assert store.load(one.id).id == one.id
    with pytest.raises(HistoryError, match="不唯一"):
        store.load("abc")
    with pytest.raises(HistoryError):
        store.load("abc3")
    with pytest.raises(HistoryError):
        store.load("")
    with pytest.raises(HistoryError):
        store.load("ZZZ")


@pytest.mark.parametrize("bad", ["../escape", "..", "a/b", "a" * 33, "not-hex", "/etc/passwd"])
def test_invalid_ids_never_reach_the_filesystem(tmp_path, bad):
    root = tmp_path / "history"
    store = HistoryStore(root)
    with pytest.raises(HistoryError):
        store.load(bad)
    with pytest.raises(HistoryError):
        store.delete(bad)
    with pytest.raises(HistoryError):
        store.save_turn(Session(id=bad), turn("a", "b"), [])
    assert not root.exists()
    assert not (tmp_path / "escape").exists()


def test_symlinked_session_directory_is_rejected(tmp_path):
    root = tmp_path / "history"
    outside = tmp_path / "outside"
    outside.mkdir()
    (outside / "turns.jsonl").write_text(
        json.dumps(
            {
                "model_id": MODEL,
                "user": "secret",
                "assistant": "secret",
                "attachments": [],
                "usage": None,
                "status": "complete",
                "timestamp": "2024-01-01T00:00:00+00:00",
                "saved_at": "2024-01-01T00:00:00+00:00",
            }
        )
        + "\n",
        encoding="utf-8",
    )
    store = HistoryStore(root)
    keeper = store.new()
    store.save_turn(keeper, turn("keep", "me"), [])
    sid = "b" * 32
    os.symlink(outside, root / sid)

    with pytest.raises(HistoryError):
        store.load(sid)
    assert [item.id for item in store.list()] == [keeper.id]
    with pytest.raises(HistoryError):
        store.delete(sid)
    assert outside.is_dir() and (outside / "turns.jsonl").exists()


def test_symlinked_attachment_paths_are_never_written_or_returned(tmp_path):
    root = tmp_path / "history"
    outside = tmp_path / "outside"
    outside.mkdir()
    (outside / "secret.txt").write_text("secret", encoding="utf-8")
    store = HistoryStore(root, save_images=True)
    session = store.new("symlink")
    store.save_turn(session, turn("a", "b"), [])
    os.symlink(outside, root / session.id / "attachments")
    with pytest.raises(HistoryError):
        store.save_turn(
            session,
            turn("c", "d", attachments=[Attachment("x.png", "png", 1, 1, 1)]),
            [b"x"],
        )
    assert sorted(path.name for path in outside.iterdir()) == ["secret.txt"]

    os.unlink(root / session.id / "attachments")
    session = store.new("symlink-file")
    store.save_turn(
        session,
        turn("e", "f", attachments=[Attachment("y.png", "png", 1, 1, 1)]),
        [b"y"],
    )
    target = root / session.id / "attachments" / "0001-01.png"
    target.unlink()
    os.symlink(outside / "secret.txt", target)
    loaded = store.load(session.id)  # loading never reads attachments
    assert loaded.turns[0].attachments[0].file == "attachments/0001-01.png"
    with pytest.raises(HistoryError):
        store.attachment_path(loaded, 1, 1)
    assert (outside / "secret.txt").read_text(encoding="utf-8") == "secret"


def test_failed_attachment_write_rolls_back_files_and_record(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root, save_images=True)
    session = store.new()
    item = turn(
        "中途失败",
        "partial",
        attachments=[Attachment("a.png", "png", 1, 1, 2), Attachment("b.png", "png", 1, 1, 2)],
    )
    with pytest.raises(HistoryError):
        store.save_turn(session, item, [b"ok", "not-bytes"])
    assert session.turns == []
    assert records(root, session.id) == []
    attachments = root / session.id / "attachments"
    assert not attachments.exists() or list(attachments.iterdir()) == []


def test_attachment_directory_symlink_is_rejected_on_reuse(tmp_path):
    root = tmp_path / "history"
    outside = tmp_path / "outside"
    outside.mkdir()
    store = HistoryStore(root, save_images=True)
    session = store.new()
    store.save_turn(
        session,
        turn("dir", "symlink", attachments=[Attachment("z.png", "png", 1, 1, 1)]),
        [b"z"],
    )
    real = root / session.id / "attachments"
    real.rename(root / session.id / "attachments.real")
    os.symlink(outside, real)
    loaded = store.load(session.id)
    with pytest.raises(HistoryError):
        store.attachment_path(loaded, 1, 1)
    assert list(outside.iterdir()) == []


def test_partial_os_writes_are_completed(tmp_path, monkeypatch):
    import bedrock_chat.history as history

    real_write = os.write

    def trickle(fd, data):
        return real_write(fd, bytes(data)[:1])

    monkeypatch.setattr(history.os, "write", trickle)
    root = tmp_path / "history"
    store = HistoryStore(root)
    session = store.new()
    text = "長いテキスト" * 40
    store.save_turn(session, turn(text, "reply" * 40), [])
    stored = records(root, session.id)
    assert len(stored) == 1 and stored[0]["user"] == text


def test_delete_removes_only_the_owned_session(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root)
    doomed = store.new("消す")
    keeper = store.new("残す")
    store.save_turn(doomed, turn("a", "b"), [])
    store.save_turn(keeper, turn("c", "d"), [])
    outside = tmp_path / "unrelated"
    outside.mkdir()

    store.delete(doomed.id[:8])
    assert not (root / doomed.id).exists()
    assert (root / keeper.id / "turns.jsonl").exists()
    assert outside.is_dir()
    with pytest.raises(HistoryError):
        store.delete(doomed.id)


# ------------------------------------------------------------ disabled history


def test_disabled_store_writes_nothing_but_keeps_memory(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root, enabled=False, save_images=True)
    session = store.new("保持", model_id=MODEL)
    item = turn("a", "b", attachments=[Attachment("x.png", "png", 1, 1, 1)])
    store.save_turn(session, item, [b"ignored"])
    store.save_turn(session, item, [b"ignored"])
    store.save_state(session)
    assert len(session.turns) == 2
    assert not root.exists()
    assert not tmp_path.joinpath("history").exists()

    for call in (
        lambda: store.load(session.id),
        lambda: store.list(),
        lambda: store.rename(session.id, "no"),
        lambda: store.delete(session.id),
    ):
        with pytest.raises(HistoryError, match="禁用"):
            call()
    assert not root.exists()


def test_disabled_export_is_the_only_allowed_write(tmp_path):
    store = HistoryStore(tmp_path / "history", enabled=False)
    session = store.new("エクスポート")
    store.save_turn(session, turn("書き出し", "done"), [])
    target = store.export(session, tmp_path / "session.md")
    assert target.read_text(encoding="utf-8").startswith("# エクスポート")
    assert modes(target) == 0o600
    assert not (tmp_path / "history").exists()


# ------------------------------------------------------------------- exporting


def test_export_is_markdown_and_never_overwrites(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root, save_images=True)
    session = store.new("导出 / export")
    store.save_turn(
        session,
        turn(
            "请分析这张图",
            "好的。",
            attachments=[Attachment("pic.png", "png", 3, 4, 5)],
            usage={"input_tokens": 7, "output_tokens": 8, "total_tokens": 15},
        ),
        [b"img"],
    )
    store.save_turn(session, turn("失败的一轮", "", status="incomplete"), [])

    target = store.export(session, tmp_path / "chat.md")
    text = target.read_text(encoding="utf-8")
    assert "请分析这张图" in text and "好的。" in text
    assert "`attachments/0001-01.png`" in text
    assert "未完成: incomplete" in text
    assert "完整回复: 1 轮" in text and "未完成/失败: 1 轮" in text
    assert "input_tokens=7" in text and "total_tokens=15" in text
    assert not (tmp_path / "chat.assets").exists()  # nothing is copied
    assert not (tmp_path / "attachments").exists()

    with pytest.raises(HistoryError, match="已存在"):
        store.export(session, target)
    assert target.read_text(encoding="utf-8") == text

    with pytest.raises(HistoryError, match="目录"):
        store.export(session, tmp_path / "missing" / "chat.md")


# ---------------------------------------------------------------- validation


@pytest.mark.parametrize(
    "usage",
    [
        {"prompt_tokens": 1},
        {"input_tokens": 1},
        {"input_tokens": 1, "output_tokens": 2},
        {},
        {"input_tokens": -1},
        {"input_tokens": True},
        {"total_tokens": 1.5},
    ],
)
def test_invalid_usage_is_rejected(tmp_path, usage):
    store = HistoryStore(tmp_path / "history")
    session = store.new()
    with pytest.raises(HistoryError):
        store.save_turn(session, turn("a", "b", usage=usage), [])
    assert session.turns == []


def test_invalid_attachment_metadata_is_rejected(tmp_path):
    store = HistoryStore(tmp_path / "history")
    session = store.new()
    for attachment in (
        Attachment("a/b.png", "png", 1, 1, 1),
        Attachment("ok.png", "p/ng", 1, 1, 1),
        Attachment("ok.png", "png", -1, 1, 1),
        Attachment("", "png", 1, 1, 1),
    ):
        with pytest.raises(HistoryError):
            store.save_turn(session, turn("a", "b", attachments=[attachment]), [])
    assert session.turns == []
    with pytest.raises(HistoryError):
        store.save_turn(session, turn("a", "b", status="Not Valid"), [])


def test_invalid_session_fields_are_rejected_before_writing(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root)
    bad = Session(name="bad\x1bname")
    with pytest.raises(HistoryError):
        store.save_turn(bad, turn("a", "b"), [])
    with pytest.raises(HistoryError):
        store.save_state(Session(context_start=-1))
    with pytest.raises(HistoryError):
        store.save_state(Session(created_at=""))
    assert not root.exists()


def test_unicode_line_separators_are_not_line_breaks(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root)
    session = store.new("区切り")
    user = "前\u2028中\u2029後\u0085終"
    assistant = "返事\u2028です\u2029。"
    store.save_turn(session, turn(user, assistant), [])
    store.save_turn(session, turn("second", "ok"), [])
    assert len(records(root, session.id)) == 2
    loaded = store.load(session.id)
    assert loaded.turns[0].user == user
    assert loaded.turns[0].assistant == assistant
    assert loaded.turns[1].user == "second"


def test_corrupt_jsonl_is_reported(tmp_path):
    root = tmp_path / "history"
    store = HistoryStore(root)
    session = store.new()
    store.save_turn(session, turn("a", "b"), [])
    path = root / session.id / "turns.jsonl"
    path.write_text(path.read_text(encoding="utf-8") + "{not json\n", encoding="utf-8")
    with pytest.raises(HistoryError, match="损坏"):
        store.load(session.id)


def test_saving_a_turn_does_not_revert_a_concurrent_rename(tmp_path):
    root = tmp_path / "history"
    writer = HistoryStore(root)
    stale = writer.new("旧标题")
    writer.save_turn(stale, turn("one", "1"), [])

    other = HistoryStore(root)
    renamed = other.load(stale.id)
    renamed.name = "新しい題名"
    renamed.context_start = 1
    renamed.model_id = "switched-model"
    other.save_state(renamed)
    writer.save_turn(stale, turn("two", "2"), [])

    reloaded = other.load(stale.id)
    assert reloaded.name == "新しい題名"
    assert reloaded.context_start == 1 and reloaded.model_id == "switched-model"
    assert len(reloaded.turns) == 2
    # a brand new session still persists its initial title on the first turn
    fresh = HistoryStore(root).new("初回タイトル")
    HistoryStore(root).save_turn(fresh, turn("first", "x"), [])
    assert HistoryStore(root).load(fresh.id).name == "初回タイトル"


# --------------------------------------------------------------- concurrency


def test_concurrent_appends_from_several_handles_lose_nothing(tmp_path):
    root = tmp_path / "history"
    seed = HistoryStore(root).new("并发")
    sid = seed.id
    HistoryStore(root).save_turn(seed, turn("seed", "seed"), [])
    workers, per_worker = 8, 5
    errors: list[BaseException] = []

    def worker(number: int) -> None:
        try:
            store = HistoryStore(root)
            session = Session(id=sid)
            for index in range(per_worker):
                store.save_turn(session, turn(f"w{number}-{index}", "ok"), [])
        except BaseException as error:  # pragma: no cover - reported below
            errors.append(error)

    threads = [threading.Thread(target=worker, args=(number,)) for number in range(workers)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()

    assert errors == []
    stored = records(root, sid)
    assert len(stored) == 1 + workers * per_worker
    users = {record["user"] for record in stored}
    assert len(users) == len(stored)
    assert HistoryStore(root).load(sid).turns[0].user == "seed"
