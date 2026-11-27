"""Persistent chat history for bedrock-chat.

On-disk layout (everything lives under ``HistoryStore.root``)::

    <id>/state.json      atomic JSON: name, model_id, context_start, timestamps
    <id>/turns.jsonl     append-only, one JSON object per turn
    <id>/attachments/    0001-01.png, ... (only when images were saved)
    <id>/.lock           advisory lock for cross-process appends/state updates

Guarantees:

* Every directory is created with mode ``0700``, every file with ``0600``.
* Turns are appended with ``O_APPEND`` while holding an exclusive ``flock``, so
  concurrent writers cannot lose each other's lines.  ``state.json`` is always
  replaced atomically (``os.replace`` of a fsynced temporary file).
* Session ids are restricted to lowercase hex, so an id or path from a user
  cannot escape the root directory.  Existing symlinks below the root are
  rejected instead of followed.
* When history is disabled the store never touches the filesystem, except for
  an explicit :meth:`HistoryStore.export`.
"""

from __future__ import annotations

import fcntl
import json
import os
import re
import shutil
import stat
import uuid
from contextlib import contextmanager
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Iterator, Sequence

from .config import ConfigError, validate_model

STATUS_COMPLETE = "complete"
_USAGE_KEYS = {"input_tokens", "output_tokens", "total_tokens"}
_LAST_MODEL = "last-model"

_ID_RE = re.compile(r"[0-9a-f]{32}")
_PREFIX_RE = re.compile(r"[0-9a-f]{1,32}")
_FORMAT_RE = re.compile(r"[A-Za-z0-9]{1,16}")
_STATUS_RE = re.compile(r"[a-z_]{1,32}")
_CONTROL_RE = re.compile(r"[\x00-\x1f\x7f-\x9f]")
_SAFE_REL_RE = re.compile(r"attachments/[0-9]{4,}-[0-9]{2,}\.[a-z0-9]{1,16}")


class HistoryError(Exception):
    """Raised for invalid sessions, unsafe paths and unreadable history data."""


def utc_now() -> str:
    """Current UTC time in ISO-8601, used for the default timestamps."""
    return datetime.now(timezone.utc).isoformat(timespec="microseconds")


def default_root() -> Path:
    """Default history root: the caller's XDG state directory."""
    from .config import xdg_path

    return xdg_path("XDG_STATE_HOME", ".local/state")


@dataclass
class Attachment:
    name: str
    format: str
    width: int
    height: int
    size: int
    file: str | None = None


@dataclass
class Turn:
    model_id: str
    user: str
    assistant: str
    attachments: list[Attachment] = field(default_factory=list)
    usage: dict[str, int] | None = None
    status: str = STATUS_COMPLETE
    timestamp: str = field(default_factory=utc_now)


@dataclass
class Session:
    id: str = field(default_factory=lambda: uuid.uuid4().hex)
    name: str = "未命名"
    turns: list[Turn] = field(default_factory=list)
    context_start: int = 0
    model_id: str | None = None
    created_at: str = field(default_factory=utc_now)


class HistoryStore:
    """Store chat sessions below ``root``.

    ``load``/``list``/``rename``/``delete`` raise :class:`HistoryError` when the
    store is disabled, because there is nothing on disk to operate on.
    ``new``/``save_turn``/``save_state`` keep working in memory, so the chat loop
    can be written once for both modes.
    """

    def __init__(self, root: Path, *, enabled: bool = True, save_images: bool = False) -> None:
        self.root = Path(root)
        self.enabled = bool(enabled)
        self.save_images = bool(save_images)

    # ---------------------------------------------------------------- creation

    def new(self, name: str = "未命名", model_id: str | None = None) -> Session:
        """Create an in-memory session; nothing is written until a save call."""
        return Session(id=uuid.uuid4().hex, name=_valid_name(name), model_id=model_id)

    def load_last_model(self) -> str | None:
        """Read the local preference without loading any conversation."""
        if not self.enabled:
            return None
        try:
            with _dir_fd(self._root_path(create=False)) as dir_fd:
                fd = os.open(
                    _LAST_MODEL, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=dir_fd
                )
                with os.fdopen(fd, "rb") as handle:
                    if not stat.S_ISREG(os.fstat(handle.fileno()).st_mode):
                        return None
                    return validate_model(handle.read(2050).decode("ascii").removesuffix("\n"))
        except (HistoryError, OSError, UnicodeError, ConfigError):
            return None

    def save_last_model(self, model_id: str) -> None:
        """Atomically store only the resolved model ID, using private permissions."""
        if not self.enabled:
            return
        data = (validate_model(model_id) + "\n").encode("ascii")
        with _dir_fd(self._root_path(create=True)) as dir_fd:
            _atomic_write_at(dir_fd, _LAST_MODEL, data)

    # ----------------------------------------------------------------- writing

    def save_turn(self, session: Session, turn: Turn, image_data: Sequence[bytes]) -> None:
        """Persist ``turn`` and append it to ``session.turns`` exactly once.

        The call is idempotent for the most recent turn: repeating it with the
        same turn (same timestamp, texts, status and attachment metadata) does
        not create a second JSONL record.

        ``image_data`` is only used when ``save_images`` is enabled, in which
        case it must contain one ``bytes`` object per attachment.  Attachment
        metadata is always written to the JSONL record; without ``save_images``
        the image payload is discarded and ``Attachment.file`` stays ``None``.

        On failure nothing is added to ``session.turns`` and a
        :class:`HistoryError` is raised.  A failed attachment write removes the
        files written so far, and a failed append truncates the partial line
        again; the JSONL record is only built after all attachment files were
        written, so it always contains their generated relative paths and never
        points at missing data.

        ``save_turn`` only refreshes ``updated_at`` in ``state.json``; title,
        ``context_start`` and ``model_id`` are owned by :meth:`save_state` and
        :meth:`rename`, so a session loaded before a concurrent rename cannot
        write a stale title back.
        """
        if not isinstance(session, Session) or not isinstance(turn, Turn):
            raise TypeError("session 与 turn 必须是 Session/Turn 实例。")
        _validate_session_fields(session)
        _validate_turn(turn)
        if not self.enabled:
            session.turns.append(turn)
            return
        sid = _require_id(session.id)
        with self._lock(sid):
            records = self._read_records(sid)
            duplicate = bool(records) and _signature(_turn_from_record(records[-1])) == _signature(
                turn
            )
            if duplicate:
                _restore_files(turn, records[-1])
            else:
                written: list[Path] = []
                try:
                    if self.save_images:
                        self._write_attachments(sid, len(records) + 1, turn, image_data, written)
                    # Build the record only after the images were written so it
                    # stores the generated relative paths, not the caller's None.
                    self._append_record(sid, _record_from_turn(turn))
                except BaseException:
                    for path in written:
                        try:
                            path.unlink()
                        except OSError:
                            pass
                    raise
            self._touch_state(session)
        if not duplicate:
            session.turns.append(turn)

    def save_state(self, session: Session) -> None:
        """Persist title, ``context_start`` and ``model_id`` (used by /clear etc.).

        This is an explicit overwrite of those three fields (last writer wins);
        ``save_turn`` never touches them.
        """
        if not isinstance(session, Session):
            raise TypeError("session 必须是 Session 实例。")
        _validate_session_fields(session)
        if not self.enabled:
            return
        sid = _require_id(session.id)
        with self._lock(sid):
            self._write_state(session)

    def rename(self, id_or_prefix: str, name: str) -> Session:
        """Rename a session and return the reloaded session."""
        if not self.enabled:
            raise HistoryError("历史记录已禁用，无法重命名会话。")
        sid = self._resolve(id_or_prefix)
        name = _valid_name(name)
        with self._lock(sid):
            session = self._load_existing(sid)
            session.name = name
            self._write_state(session)
        return self._load_existing(sid)

    def delete(self, id_or_prefix: str) -> None:
        """Delete every file owned by the session (never outside the root)."""
        if not self.enabled:
            raise HistoryError("历史记录已禁用，无法删除会话。")
        sid = self._resolve(id_or_prefix)
        path = self._session_path(sid)
        try:
            info = os.lstat(path)
        except FileNotFoundError:
            raise HistoryError("找不到该会话。") from None
        with self._lock(sid):
            if stat.S_ISLNK(info.st_mode):
                os.unlink(path)
            elif stat.S_ISDIR(info.st_mode):
                shutil.rmtree(path)
            else:
                raise HistoryError("会话路径不是目录。")

    # ----------------------------------------------------------------- reading

    def load(self, id_or_prefix: str) -> Session:
        """Load a session by full id or unambiguous prefix."""
        if not self.enabled:
            raise HistoryError("历史记录已禁用，无法读取会话。")
        return self._load_existing(self._resolve(id_or_prefix))

    def list(self, query: str = "") -> list[Session]:
        """List sessions ordered by last update (newest first).

        With a query, the session name, model id, user/assistant texts and the
        attachment names are searched case-insensitively and the matching turns
        are included.  Without a query, sessions are returned *without* turns
        (``turns == []``) to keep listing cheap; call :meth:`load` for details.
        """
        if not self.enabled:
            raise HistoryError("历史记录已禁用，无法列出会话。")
        if not isinstance(query, str):
            raise TypeError("query 必须是字符串。")
        needle = query.casefold()
        found: list[tuple[str, Session]] = []
        for sid in self._session_ids():
            try:
                state = self._read_state(sid) or {}
                records = self._read_records(sid)
            except HistoryError:
                continue
            turns = [_turn_from_record(record) for record in records]
            session = _session_from_state(sid, state, turns)
            updated = state.get("updated_at") or state.get("created_at") or session.created_at
            if needle and not _matches(session, turns, needle):
                continue
            found.append((updated, session))
        found.sort(key=lambda item: item[0], reverse=True)
        result = []
        for _, session in found:
            if not needle:
                session.turns = []
            result.append(session)
        return result

    # ------------------------------------------------------------- attachments

    def attachment_path(self, session: Session, turn_number: int, image_number: int) -> Path:
        """Return the file path for an attachment (1-based turn and image).

        This is an explicit follow-up call: reading a session or listing turns
        never opens attachment files, and the path is returned without checking
        whether the file exists.
        """
        if not isinstance(session, Session):
            raise TypeError("session 必须是 Session 实例。")
        if type(turn_number) is not int or type(image_number) is not int:
            raise HistoryError("turn_number 与 image_number 必须是整数。")
        if not 1 <= turn_number <= len(session.turns):
            raise HistoryError("轮次超出范围。")
        attachments = session.turns[turn_number - 1].attachments
        if not 1 <= image_number <= len(attachments):
            raise HistoryError("附件序号超出范围。")
        relative = attachments[image_number - 1].file
        if relative is None:
            raise HistoryError("该附件未保存到磁盘（save_images 未启用）。")
        if not _SAFE_REL_RE.fullmatch(relative):
            raise HistoryError("附件路径无效。")
        directory = self._existing_session_dir(_require_id(session.id)) / "attachments"
        try:
            info = os.lstat(directory)
        except FileNotFoundError:
            raise HistoryError("附件目录不存在。") from None
        except OSError:
            raise HistoryError("无法读取附件目录。") from None
        if not stat.S_ISDIR(info.st_mode):
            raise HistoryError("附件路径不是普通目录。")
        name = relative.split("/", 1)[1]
        with _dir_fd(directory) as dir_fd:
            try:
                info = os.stat(name, dir_fd=dir_fd, follow_symlinks=False)
            except FileNotFoundError:
                info = None
            except OSError:
                raise HistoryError("无法读取附件。") from None
            if info is not None and not stat.S_ISREG(info.st_mode):
                raise HistoryError("附件不是普通文件。")
        return directory / name

    # ------------------------------------------------------------------ export

    def export(self, session: Session, path: Path) -> Path:
        """Write a Markdown transcript to ``path``; never overwrite a file.

        This is the only operation that writes while history is disabled, and it
        always requires an explicit path.  The parent directory must already
        exist; attachment files are referenced by name only, nothing is copied.
        """
        if not isinstance(session, Session):
            raise TypeError("session 必须是 Session 实例。")
        target = Path(path)
        if target.exists():
            raise HistoryError("导出目标已存在，未覆盖。")
        parent = target.parent
        if not parent.is_dir():
            raise HistoryError("导出目录不存在。")
        updated = ""
        if self.enabled:
            try:
                state = self._read_state(_require_id(session.id))
            except HistoryError:
                state = None
            updated = (state or {}).get("updated_at", "")
        text = _render_markdown(session, updated)
        try:
            handle = open(  # noqa: SIM115 - closed explicitly below
                target,
                "x",
                encoding="utf-8",
                opener=lambda name, flags: os.open(name, flags | os.O_NOFOLLOW, 0o600),
            )
        except FileExistsError:
            raise HistoryError("导出目标已存在，未覆盖。") from None
        except OSError:
            raise HistoryError("无法写入导出文件。") from None
        try:
            with handle:
                handle.write(text)
                handle.flush()
                os.fsync(handle.fileno())
        except OSError:
            try:
                target.unlink()
            except OSError:
                pass
            raise HistoryError("无法写入导出文件。") from None
        try:
            os.chmod(target, 0o600)
        except OSError:
            raise HistoryError("无法设置导出文件权限。") from None
        return target

    # ---------------------------------------------------------------- internals

    def _root_path(self, *, create: bool) -> Path:
        root = self.root
        try:
            info = os.lstat(root)
        except FileNotFoundError:
            info = None
        if info is not None:
            if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
                raise HistoryError("历史目录不是普通目录。")
            if not os.access(root, os.R_OK | os.W_OK | os.X_OK):
                raise HistoryError("历史目录不可读写。")
        elif not create:
            return root
        if create:
            try:
                root.mkdir(mode=0o700, parents=True, exist_ok=True)
            except OSError:
                raise HistoryError("无法创建历史目录。") from None
            _harden_dir(root)
        return root

    def _session_path(self, sid: str) -> Path:
        root = self._root_path(create=False)
        path = root / _require_id(sid)
        if path.exists() and not path.resolve().is_relative_to(root.resolve()):
            raise HistoryError("会话路径越界。")
        return path

    def _ensure_session_dir(self, sid: str) -> Path:
        root = self._root_path(create=True)
        path = root / sid
        for _ in range(2):
            try:
                info = os.lstat(path)
            except FileNotFoundError:
                info = None
            if info is not None:
                if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
                    raise HistoryError("会话路径不是普通目录。")
                break
            try:
                path.mkdir(mode=0o700)
            except FileExistsError:
                # Another process created it between lstat and mkdir.
                continue
            except OSError:
                raise HistoryError("无法创建会话目录。") from None
        else:
            raise HistoryError("无法创建会话目录。")
        _harden_dir(path)
        if not path.resolve().is_relative_to(root.resolve()):
            raise HistoryError("会话路径越界。")
        return path

    def _existing_session_dir(self, sid: str) -> Path:
        path = self._root_path(create=False) / sid
        try:
            info = os.lstat(path)
        except FileNotFoundError:
            raise HistoryError("找不到该会话。") from None
        if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
            raise HistoryError("会话路径不是普通目录。")
        if not path.resolve().is_relative_to(self.root.resolve()):
            raise HistoryError("会话路径越界。")
        return path

    def _session_ids(self) -> list[str]:
        root = self._root_path(create=False)
        try:
            entries = list(root.iterdir())
        except FileNotFoundError:
            return []
        except OSError:
            raise HistoryError("无法读取历史目录。") from None
        ids = []
        for entry in entries:
            if not _ID_RE.fullmatch(entry.name):
                continue
            try:
                if not entry.is_symlink() and entry.is_dir():
                    ids.append(entry.name)
            except OSError:
                continue
        return ids

    def _resolve(self, id_or_prefix: str) -> str:
        if not isinstance(id_or_prefix, str) or not _PREFIX_RE.fullmatch(id_or_prefix):
            raise HistoryError("会话 ID 或前缀格式无效。")
        matches = [sid for sid in self._session_ids() if sid.startswith(id_or_prefix)]
        if not matches:
            raise HistoryError("找不到该会话。")
        if len(matches) > 1:
            raise HistoryError("会话前缀不唯一，请提供更多字符。")
        return matches[0]

    @contextmanager
    def _lock(self, sid: str) -> Iterator[None]:
        directory = self._ensure_session_dir(sid)
        lock = directory / ".lock"
        try:
            fd = os.open(lock, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600)
        except OSError:
            raise HistoryError("无法创建会话锁文件。") from None
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            os.close(fd)
            raise HistoryError("会话锁文件不是普通文件。")
        try:
            os.fchmod(fd, 0o600)
        except OSError:
            os.close(fd)
            raise HistoryError("无法设置会话锁文件权限。") from None
        try:
            fcntl.flock(fd, fcntl.LOCK_EX)
            yield
        finally:
            try:
                fcntl.flock(fd, fcntl.LOCK_UN)
            finally:
                os.close(fd)

    def _read_records(self, sid: str) -> list[dict]:
        directory = self._existing_session_dir(sid)
        with _dir_fd(directory) as dir_fd:
            try:
                info = os.stat("turns.jsonl", dir_fd=dir_fd, follow_symlinks=False)
            except FileNotFoundError:
                return []
            except OSError:
                raise HistoryError("无法读取历史记录。") from None
            if not stat.S_ISREG(info.st_mode):
                raise HistoryError("历史记录不是普通文件。")
            try:
                fd = os.open("turns.jsonl", os.O_RDONLY | os.O_NOFOLLOW, dir_fd=dir_fd)
                with os.fdopen(fd, "r", encoding="utf-8", newline="") as handle:
                    text = handle.read()
            except (OSError, UnicodeError):
                raise HistoryError("无法读取历史记录。") from None
        records = []
        # Never splitlines(): it also splits on U+2028/U+2029/U+0085, which are
        # legal unescaped characters inside a JSON string.
        for line in text.split("\n"):
            line = line.strip()
            if not line:
                continue
            try:
                record = json.loads(line)
            except ValueError:
                raise HistoryError("历史记录已损坏。") from None
            records.append(_validate_record(record))
        return records

    def _write_attachments(
        self,
        sid: str,
        turn_index: int,
        turn: Turn,
        image_data: Sequence[bytes],
        written: list[Path],
    ) -> None:
        if len(image_data) != len(turn.attachments):
            raise HistoryError("图片数据数量与附件数量不一致。")
        if not turn.attachments:
            return
        directory = self._attachments_dir(sid)
        with _dir_fd(directory) as dir_fd:
            for number, (attachment, data) in enumerate(
                zip(turn.attachments, image_data, strict=True), 1
            ):
                if not isinstance(data, (bytes, bytearray, memoryview)):
                    raise HistoryError("图片数据必须是 bytes。")
                name = f"{turn_index:04d}-{number:02d}.{attachment.format.lower()}"
                try:
                    info = os.stat(name, dir_fd=dir_fd, follow_symlinks=False)
                except FileNotFoundError:
                    info = None
                except OSError:
                    raise HistoryError("无法检查附件路径。") from None
                if info is not None and not stat.S_ISREG(info.st_mode):
                    raise HistoryError("附件路径不是普通文件。")
                _atomic_write_at(dir_fd, name, bytes(data))
                written.append(directory / name)
                attachment.file = f"attachments/{name}"

    def _attachments_dir(self, sid: str) -> Path:
        directory = self._ensure_session_dir(sid) / "attachments"
        for _ in range(2):
            try:
                info = os.lstat(directory)
            except FileNotFoundError:
                info = None
            if info is not None:
                if not stat.S_ISDIR(info.st_mode):
                    raise HistoryError("附件路径不是普通目录。")
                break
            try:
                directory.mkdir(mode=0o700)
            except FileExistsError:
                continue
            except OSError:
                raise HistoryError("无法创建附件目录。") from None
        else:
            raise HistoryError("无法创建附件目录。")
        _harden_dir(directory)
        return directory

    def _append_record(self, sid: str, record: dict) -> None:
        directory = self._ensure_session_dir(sid)
        line = (json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n").encode()
        with _dir_fd(directory) as dir_fd:
            try:
                fd = os.open(
                    "turns.jsonl",
                    os.O_WRONLY | os.O_CREAT | os.O_APPEND | os.O_NOFOLLOW,
                    0o600,
                    dir_fd=dir_fd,
                )
            except OSError:
                raise HistoryError("无法写入历史记录。") from None
            try:
                size = os.fstat(fd).st_size
                try:
                    _write_all(fd, line)
                    os.fsync(fd)
                except (OSError, HistoryError):
                    try:  # drop a partially written line instead of corrupting JSONL
                        os.ftruncate(fd, size)
                    except OSError:
                        pass
                    raise HistoryError("无法写入历史记录。") from None
            finally:
                os.close(fd)
            try:
                os.chmod("turns.jsonl", 0o600, dir_fd=dir_fd)
            except OSError:
                raise HistoryError("无法设置历史文件权限。") from None
            try:
                os.fsync(dir_fd)
            except OSError:
                pass

    def _read_state(self, sid: str) -> dict | None:
        directory = self._existing_session_dir(sid)
        with _dir_fd(directory) as dir_fd:
            try:
                info = os.stat("state.json", dir_fd=dir_fd, follow_symlinks=False)
            except FileNotFoundError:
                return None
            except OSError:
                raise HistoryError("无法读取会话状态。") from None
            if not stat.S_ISREG(info.st_mode):
                raise HistoryError("会话状态不是普通文件。")
            try:
                fd = os.open("state.json", os.O_RDONLY | os.O_NOFOLLOW, dir_fd=dir_fd)
                with os.fdopen(fd, "r", encoding="utf-8") as handle:
                    state = json.load(handle)
            except (OSError, UnicodeError, ValueError):
                raise HistoryError("会话状态已损坏。") from None
        return _validate_state(state)

    def _write_state(self, session: Session) -> None:
        sid = _require_id(session.id)
        directory = self._ensure_session_dir(sid)
        _atomic_write_file(directory, "state.json", _state_payload(session))

    def _touch_state(self, session: Session) -> None:
        """Refresh ``updated_at`` without reverting a concurrent rename//clear.

        ``save_turn`` only owns the update timestamp; title, context and model
        are written exclusively by :meth:`save_state`/:meth:`rename`.  Keeping
        the on-disk values here means a session loaded before another process
        renamed it cannot silently write the stale title back.
        """
        sid = _require_id(session.id)
        directory = self._ensure_session_dir(sid)
        try:
            existing = self._read_state(sid)
        except HistoryError:
            existing = None
        if existing is None:
            state = _state_payload(session)
        else:
            state = {**existing, "updated_at": utc_now()}
        _atomic_write_file(directory, "state.json", state)

    def _load_existing(self, sid: str) -> Session:
        state = self._read_state(sid) or {}
        records = self._read_records(sid)
        return _session_from_state(sid, state, [_turn_from_record(r) for r in records])


# --------------------------------------------------------------------- helpers


def _harden_dir(path: Path) -> None:
    try:
        mode = stat.S_IMODE(os.lstat(path).st_mode)
        if mode != 0o700:
            os.chmod(path, 0o700)
    except OSError:
        raise HistoryError("无法设置历史目录权限。") from None


def _state_payload(session: Session) -> dict:
    return {
        "id": _require_id(session.id),
        "name": session.name,
        "model_id": session.model_id,
        "context_start": session.context_start,
        "created_at": session.created_at,
        "updated_at": utc_now(),
    }


@contextmanager
def _dir_fd(path: Path) -> Iterator[int]:
    """Open ``path`` as a directory without following a final symlink."""
    try:
        fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    except OSError:
        raise HistoryError("无法打开历史目录。") from None
    try:
        yield fd
    finally:
        os.close(fd)


def _write_all(fd: int, data: bytes) -> None:
    view = memoryview(data)
    while view:
        try:
            written = os.write(fd, view)
        except OSError:
            raise HistoryError("无法写入历史文件。") from None
        if written <= 0:
            raise HistoryError("无法写入历史文件。")
        view = view[written:]


def _atomic_write_at(dir_fd: int, name: str, data: bytes) -> None:
    """Write ``name`` inside ``dir_fd`` via a fsynced temporary file."""
    temp = f".{name}.{uuid.uuid4().hex}.tmp"
    try:
        fd = os.open(
            temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=dir_fd
        )
    except OSError:
        raise HistoryError("无法写入历史文件。") from None
    try:
        try:
            _write_all(fd, data)
            os.fsync(fd)
        finally:
            os.close(fd)
        os.rename(temp, name, src_dir_fd=dir_fd, dst_dir_fd=dir_fd)
        try:
            os.fsync(dir_fd)
        except OSError:
            pass
    except (OSError, HistoryError):
        try:
            os.unlink(temp, dir_fd=dir_fd)
        except OSError:
            pass
        raise HistoryError("无法写入历史文件。") from None


def _atomic_write_file(directory: Path, name: str, payload: dict) -> None:
    data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    with _dir_fd(directory) as dir_fd:
        _atomic_write_at(dir_fd, name, data)


def _require_id(value: object) -> str:
    if not isinstance(value, str) or not _ID_RE.fullmatch(value):
        raise HistoryError("会话 ID 必须是 32 位小写十六进制字符串。")
    return value


def _valid_name(name: object) -> str:
    if not isinstance(name, str) or not name.strip() or len(name) > 200:
        raise HistoryError("会话名称必须是 1～200 字符的文本。")
    if _CONTROL_RE.search(name):
        raise HistoryError("会话名称不能包含控制字符。")
    return name


def _validate_session_fields(session: Session) -> None:
    _valid_name(session.name)
    _non_negative_int(session.context_start, "context_start")
    if session.model_id is not None and (
        not isinstance(session.model_id, str) or not session.model_id
    ):
        raise HistoryError("model_id 必须是非空字符串或 None。")
    if (
        not isinstance(session.created_at, str)
        or not session.created_at
        or _CONTROL_RE.search(session.created_at)
    ):
        raise HistoryError("created_at 必须是有效的 UTC 时间字符串。")


def _non_negative_int(value: object, label: str) -> int:
    if type(value) is not int or value < 0:
        raise HistoryError(f"{label} 必须是非负整数。")
    return value


def _validate_attachment(attachment: object) -> Attachment:
    if not isinstance(attachment, Attachment):
        raise HistoryError("附件必须是 Attachment 实例。")
    if (
        not isinstance(attachment.name, str)
        or not attachment.name.strip()
        or _CONTROL_RE.search(attachment.name)
        or "/" in attachment.name
        or "\\" in attachment.name
    ):
        raise HistoryError("附件名称无效。")
    if not isinstance(attachment.format, str) or not _FORMAT_RE.fullmatch(attachment.format):
        raise HistoryError("附件格式无效。")
    _non_negative_int(attachment.width, "附件宽度")
    _non_negative_int(attachment.height, "附件高度")
    _non_negative_int(attachment.size, "附件大小")
    if attachment.file is not None and not _SAFE_REL_RE.fullmatch(attachment.file):
        raise HistoryError("附件路径无效。")
    return attachment


def _validate_usage(usage: object) -> dict[str, int] | None:
    if usage is None:
        return None
    if not isinstance(usage, dict) or set(usage) != _USAGE_KEYS:
        raise HistoryError("usage 必须完整包含 input_tokens/output_tokens/total_tokens。")
    return {key: _non_negative_int(value, key) for key, value in usage.items()}


def _validate_turn(turn: object) -> None:
    if not isinstance(turn, Turn):
        raise HistoryError("turn 必须是 Turn 实例。")
    if not isinstance(turn.model_id, str) or not turn.model_id:
        raise HistoryError("model_id 必须是非空字符串。")
    for label, text in (("用户消息", turn.user), ("助手回复", turn.assistant)):
        if not isinstance(text, str):
            raise HistoryError(f"{label}必须是字符串。")
    if not isinstance(turn.status, str) or not _STATUS_RE.fullmatch(turn.status):
        raise HistoryError("status 格式无效。")
    if not isinstance(turn.timestamp, str) or not turn.timestamp:
        raise HistoryError("timestamp 必须是非空字符串。")
    for attachment in turn.attachments:
        _validate_attachment(attachment)
    _validate_usage(turn.usage)


def _record_from_turn(turn: Turn) -> dict:
    _validate_turn(turn)
    return {
        "model_id": turn.model_id,
        "user": turn.user,
        "assistant": turn.assistant,
        "attachments": [
            {
                "name": attachment.name,
                "format": attachment.format,
                "width": attachment.width,
                "height": attachment.height,
                "size": attachment.size,
                "file": attachment.file,
            }
            for attachment in turn.attachments
        ],
        "usage": turn.usage,
        "status": turn.status,
        "timestamp": turn.timestamp,
        "saved_at": utc_now(),
    }


def _validate_record(record: object) -> dict:
    if not isinstance(record, dict):
        raise HistoryError("历史记录已损坏。")
    allowed = {
        "model_id",
        "user",
        "assistant",
        "attachments",
        "usage",
        "status",
        "timestamp",
        "saved_at",
    }
    if record.keys() - allowed:
        raise HistoryError("历史记录已损坏。")
    for key in ("model_id", "user", "assistant", "status", "timestamp", "saved_at"):
        if not isinstance(record.get(key), str):
            raise HistoryError("历史记录已损坏。")
    attachments = record.setdefault("attachments", [])
    if not isinstance(attachments, list):
        raise HistoryError("历史记录已损坏。")
    for attachment in attachments:
        if not isinstance(attachment, dict) or set(attachment) != {
            "name",
            "format",
            "width",
            "height",
            "size",
            "file",
        }:
            raise HistoryError("历史记录已损坏。")
        _validate_attachment(
            Attachment(
                name=attachment["name"],
                format=attachment["format"],
                width=attachment["width"],
                height=attachment["height"],
                size=attachment["size"],
                file=attachment["file"],
            )
        )
    _validate_usage(record.setdefault("usage", None))
    return record


def _signature(turn: Turn) -> str:
    payload = {
        "model_id": turn.model_id,
        "user": turn.user,
        "assistant": turn.assistant,
        "attachments": [
            [item.name, item.format, item.width, item.height, item.size]
            for item in turn.attachments
        ],
        "usage": turn.usage,
        "status": turn.status,
        "timestamp": turn.timestamp,
    }
    return json.dumps(payload, ensure_ascii=False, sort_keys=True)


def _restore_files(turn: Turn, record: dict) -> None:
    """Recover store-generated attachment paths for an already saved turn."""
    for attachment, item in zip(turn.attachments, record.get("attachments", []), strict=False):
        if attachment.file is None:
            attachment.file = item.get("file")


def _validate_state(state: object) -> dict:
    if not isinstance(state, dict):
        raise HistoryError("会话状态已损坏。")
    if state.keys() - {"id", "name", "model_id", "context_start", "created_at", "updated_at"}:
        raise HistoryError("会话状态已损坏。")
    _valid_name(state.get("name"))
    if state.get("model_id") is not None and not isinstance(state.get("model_id"), str):
        raise HistoryError("会话状态已损坏。")
    _non_negative_int(state.get("context_start"), "context_start")
    if not isinstance(state.get("created_at"), str) or not isinstance(state.get("updated_at"), str):
        raise HistoryError("会话状态已损坏。")
    return state


def _session_from_state(sid: str, state: dict, turns: list[Turn]) -> Session:
    created = state.get("created_at") or (turns[0].timestamp if turns else utc_now())
    return Session(
        id=sid,
        name=state.get("name") or "未命名",
        turns=turns,
        context_start=state.get("context_start", 0),
        model_id=state.get("model_id"),
        created_at=created,
    )


def _turn_from_record(record: dict) -> Turn:
    attachments = [
        Attachment(
            name=item["name"],
            format=item["format"],
            width=item["width"],
            height=item["height"],
            size=item["size"],
            file=item["file"],
        )
        for item in record.get("attachments", [])
    ]
    return Turn(
        model_id=record["model_id"],
        user=record["user"],
        assistant=record["assistant"],
        attachments=attachments,
        usage=record.get("usage"),
        status=record["status"],
        timestamp=record["timestamp"],
    )


def _matches(session: Session, turns: list[Turn], needle: str) -> bool:
    values = [session.name, session.model_id or ""]
    for turn in turns:
        values.append(turn.user)
        values.append(turn.assistant)
        values.extend(attachment.name for attachment in turn.attachments)
    return any(needle in value.casefold() for value in values)


def _render_markdown(session: Session, updated: str) -> str:
    lines = [f"# {session.name}", "", f"- 会话 ID: `{session.id}`"]
    if session.model_id:
        lines.append(f"- 模型: `{session.model_id}`")
    lines.append(f"- 创建时间: {session.created_at}")
    if updated:
        lines.append(f"- 更新时间: {updated}")
    lines.append(f"- 上下文起点: {session.context_start}")
    lines.append("")
    complete = incomplete = 0
    input_tokens = output_tokens = 0
    for number, turn in enumerate(session.turns, 1):
        if turn.usage is not None:
            input_tokens += turn.usage.get("input_tokens", 0)
            output_tokens += turn.usage.get("output_tokens", 0)
        label = turn.status
        if turn.status == STATUS_COMPLETE:
            complete += 1
        else:
            incomplete += 1
            label = f"未完成: {turn.status}"
        lines += [
            f"## 第 {number} 轮（{label}）",
            "",
            f"模型: `{turn.model_id}`",
            f"时间: {turn.timestamp}",
            "",
            "### 用户",
            "",
            turn.user,
            "",
            "### 助手",
            "",
            turn.assistant,
            "",
        ]
        if turn.attachments:
            lines.append("### 附件")
            lines.append("")
            for attachment in turn.attachments:
                detail = (
                    f"{attachment.format} {attachment.width}×{attachment.height}，"
                    f"{attachment.size} 字节"
                )
                location = (
                    f"`{attachment.file}`" if attachment.file else "未保存（save_images 未启用）"
                )
                lines.append(f"- `{attachment.name}`（{detail}）— {location}")
            lines.append("")
        if turn.usage is None:
            lines.append("用量: 未提供")
        else:
            usage = ", ".join(f"{key}={value}" for key, value in sorted(turn.usage.items()))
            lines.append(f"用量: {usage}")
        lines.append("")
    lines.append("## 汇总")
    lines.append("")
    lines.append(f"- 完整回复: {complete} 轮")
    lines.append(f"- 未完成/失败: {incomplete} 轮")
    lines.append(
        f"- 输入 tokens: {input_tokens}，输出 tokens: {output_tokens}（仅统计有用量的轮次）"
    )
    return "\n".join(lines).rstrip("\n") + "\n"
