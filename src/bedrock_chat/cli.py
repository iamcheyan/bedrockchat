"""Terminal UI and local command dispatch."""

import argparse
import shlex
import sys
from collections.abc import Iterator
from contextlib import contextmanager
from dataclasses import asdict
from decimal import Decimal
from importlib.metadata import version
from pathlib import Path
from time import monotonic

from prompt_toolkit import PromptSession
from prompt_toolkit.buffer import Buffer
from prompt_toolkit.completion import WordCompleter
from prompt_toolkit.document import Document
from prompt_toolkit.enums import DEFAULT_BUFFER
from prompt_toolkit.filters import has_focus
from prompt_toolkit.key_binding import KeyBindings
from prompt_toolkit.keys import Keys
from rich.console import Console
from rich.live import Live
from rich.markdown import Markdown

from .bedrock import Bedrock, ChatError, build_request
from .composer import MARK_END, MARK_START, ImageChips, TextHistory, markers, plain_text
from .config import (
    PROVIDERS,
    ConfigError,
    Settings,
    configured_language,
    load_settings,
    read_config,
    terminal_text,
    xdg_path,
)
from .history import Attachment, HistoryError, HistoryStore, Turn
from .i18n import DEFAULT_LANGUAGE, LANGUAGES, tr, using_language
from .images import ImageAttachment, ImageError, paste_image, read_image

MAX_IMAGES = 20
# Keep aggregate base64 payload safely below common Bedrock request body limits.
MAX_IMAGE_BODY = 15_000_000
COMMANDS = {
    "/new [名称]": "创建会话",
    "/history [关键词]": "列出会话，可按名称或消息搜索",
    "/resume <ID>": "恢复或切换会话（支持唯一前缀）",
    "/show [轮数]": "查看当前会话最近的本地记录，默认 5 轮",
    "/rename <ID> <名称>": "重命名本地会话",
    "/delete <ID>": "永久删除指定会话及其已保存图片",
    "/model [别名或 ID]": "查看或切换模型",
    "/clear": "清空发送上下文，保留本地历史和用量",
    "/tokens": "查看当前会话累计用量及可估算的费用",
    "/image <路径>": "附加指定文件；路径有空格时加引号",
    "/image paste": "手动获取 Windows 剪贴板图片",
    "/image drop [序号|all]": "移除指定图片，省略序号时移除最后一张",
    "/image reuse <轮次> <序号>": "重新附加当前会话中主动保存过的历史图片",
    "/export [文件.md]": "导出当前会话为 Markdown（不覆盖现有文件）",
    "/config": "查看当前配置（不显示 AWS 凭证或系统提示词）",
    "/lang [zh|en|ja]": "查看或切换界面语言",
    "/help": "显示本帮助",
    "/exit": "退出",
}
COMMAND_NAMES = list(dict.fromkeys(command.split()[0] for command in COMMANDS))


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="termtuck",
        description=tr("支持图片、本地历史和 Token 预算的终端聊天"),
        add_help=False,
        allow_abbrev=False,
    )
    parser._positionals.title = tr("位置参数")
    parser._optionals.title = tr("选项")
    parser.add_argument("-h", "--help", action="help", help=tr("显示帮助并退出"))
    parser.add_argument(
        "--version",
        action="version",
        version=f"%(prog)s {version('bedrock-chat')}",
        help=tr("显示版本并退出"),
    )
    parser.add_argument("--config", type=Path, help=tr("显式读取指定的 TOML 文件"))
    parser.add_argument("--lang", choices=LANGUAGES, help=tr("界面语言：中文、英语或日语"))
    parser.add_argument("--provider", choices=PROVIDERS, help=tr("模型后端（默认 bedrock）"))
    parser.add_argument("--endpoint", help=tr("OpenAI 兼容后端的完整 Chat Completions URL"))
    parser.add_argument("--region", help="AWS Region")
    parser.add_argument("--profile", help="AWS Profile")
    parser.add_argument(
        "--model", dest="model_id", help=tr("模型 ID 或别名；Bedrock 也支持推理配置文件 ID/ARN")
    )
    parser.add_argument("--max-input-tokens", type=int, help=tr("本地估算的输入 Token 预算"))
    parser.add_argument("--max-output-tokens", type=int, help=tr("请求的最大输出 Token"))
    parser.add_argument("--no-history", action="store_true", help=tr("不读写历史、图片或本地偏好"))
    parser.add_argument(
        "--no-save-images", action="store_true", help=tr("不保存图片字节到本地历史")
    )
    parser.add_argument(
        "--check-clipboard", action="store_true", help=tr("仅测试并显示剪贴板图片尺寸，不联网")
    )
    return parser.parse_args(argv)


def describe_image(image: ImageAttachment) -> str:
    return f"{image.width}×{image.height} · {len(image.data) / 1024:.1f} KiB"


def _positive(value: str) -> int:
    if not value.isascii() or not value.isdecimal() or len(value) > 9 or int(value) < 1:
        raise ValueError(tr("请输入正整数序号。"))
    return int(value)


def _markdown(text: str) -> Markdown:
    return Markdown(terminal_text(text), hyperlinks=False, code_theme="monokai")


class Preview:
    """Throttle full Markdown re-rendering and leave a complete, scrollable final reply."""

    def __init__(self, console: Console):
        self.console = console
        self.parts: list[str] = []
        self.next_frame = 0.0
        self.live = (
            Live(
                console=console,
                auto_refresh=False,
                transient=True,
                redirect_stdout=False,
                redirect_stderr=False,
            )
            if console.is_terminal
            else None
        )

    @contextmanager
    def run(self) -> Iterator[None]:
        if self.live is None:
            yield
        else:
            with self.live:
                yield

    def feed(self, text: str) -> None:
        self.parts.append(text)
        now = monotonic()
        if self.live is not None and now >= self.next_frame:
            self.live.update(_markdown("".join(self.parts)), refresh=True)
            self.next_frame = now + 0.10


class Chat:
    def __init__(self, settings: Settings, console: Console, service: Bedrock | None = None):
        self.settings = settings
        self.console = console
        if service is not None:
            self.service = service
        elif settings.provider == "openai":
            from .openai_compat import OpenAICompatible

            self.service = OpenAICompatible(settings.endpoint, stream=settings.openai_stream)
        else:
            self.service = Bedrock(settings.region, settings.profile)
        self.store = HistoryStore(
            settings.conversation_dir,
            enabled=settings.history_enabled,
            save_images=settings.save_images,
        )
        self.preferences = (
            self.store
            if settings.provider == "bedrock"
            else HistoryStore(settings.history_dir, enabled=settings.history_enabled)
        )
        last_model = None if settings.model_explicit else self.store.load_last_model()
        saved_language = None if settings.language_explicit else self.preferences.load_language()
        self.language = saved_language or settings.language
        with using_language(self.language):
            self.session = self.store.new(tr("未命名"), model_id=last_model or settings.model_id)
        self.attachments: dict[str, ImageAttachment] = {}
        self._next_marker = MARK_START
        self.notice = ""
        self.draft = ""

    @property
    def images(self) -> list[ImageAttachment]:
        return list(self.attachments.values())

    def say(self, message: str, *, style: str | None = None) -> None:
        self.console.print(terminal_text(message), markup=False, highlight=False, style=style)

    def toolbar(self) -> str:
        with using_language(self.language):
            return self._toolbar()

    def _toolbar(self) -> str:
        details = "  ".join(
            f"#{i} {describe_image(image)}" for i, image in enumerate(self.images, 1)
        )
        message = tr("图片 {count} 张", count=len(self.images)) + (
            f"  {details}" if details else ""
        )
        if self.notice:
            message += f"  │  {self.notice}"
        elif not details:
            message += "  │  " + tr("Ctrl+G 粘贴截图 · Esc+Enter 换行 · Enter 发送")
        return terminal_text(message).replace("\n", " ").replace("\t", " ")

    def _check_attach(self) -> None:
        if (
            self.session.model_id
            and self.settings.model_info(self.session.model_id).vision is False
        ):
            raise ImageError(tr("当前模型不支持图片，请用 /model 切换到视觉模型。"))
        if len(self.images) >= MAX_IMAGES:
            raise ImageError(tr("最多附加 {count} 张图片。", count=MAX_IMAGES))

    def _add_image(self, image: ImageAttachment) -> str:
        if sum(len(item.data) for item in self.images) + len(image.data) > MAX_IMAGE_BODY:
            raise ImageError(tr("图片合计超过 15 MB，请移除部分图片或减少其尺寸。"))
        if self._next_marker > MARK_END:
            raise ImageError(tr("本次运行的图片标记已用完，请重启 TermTuck。"))
        marker = chr(self._next_marker)
        self._next_marker += 1
        self.attachments[marker] = image
        self.notice = tr(
            "已附加 #{count}（{image}）", count=len(self.attachments), image=describe_image(image)
        )
        return marker

    def sync_attachments(self, text: str) -> None:
        # Keep only live markers, in input order. Deleted images cannot be
        # silently restored by undo or history recall.
        active = {key: self.attachments[key] for key in markers(text) if key in self.attachments}
        self.attachments.clear()
        self.attachments.update(active)

    def _image_options(self) -> dict[str, int]:
        return {"max_edge": self.settings.max_edge, "max_bytes": self.settings.max_bytes}

    def paste(self) -> str:
        self._check_attach()
        return self._add_image(paste_image(**self._image_options()))

    def _image(self, args: list[str]) -> None:
        if not args or args == ["list"]:
            self.say(self.toolbar())
        elif args == ["paste"]:
            self.draft += self.paste()
            self.say(self.notice)
        elif args[0] == "drop" and len(args) <= 2:
            if args[1:] == ["all"]:
                self.attachments.clear()
                self.draft = ""
            else:
                keys = list(self.attachments)
                index = _positive(args[1]) if len(args) == 2 else len(keys)
                if not 1 <= index <= len(keys):
                    raise ImageError(tr("没有这个待发送图片序号。"))
                marker = keys[index - 1]
                del self.attachments[marker]
                self.draft = self.draft.replace(marker, "")
            self.say(self.toolbar())
        elif args[0] == "reuse" and len(args) == 3:
            self._check_attach()
            path = self.store.attachment_path(self.session, _positive(args[1]), _positive(args[2]))
            self.draft += self._add_image(read_image(path, **self._image_options()))
            self.say(self.notice)
        elif len(args) == 1:
            self._check_attach()
            self.draft += self._add_image(
                read_image(Path(args[0]).expanduser(), **self._image_options())
            )
            self.say(self.notice)
        else:
            raise ValueError(
                tr("用法：/image <路径> | paste | drop [序号|all] | reuse <轮次> <序号>")
            )

    def _tokens(self) -> None:
        input_tokens = output_tokens = total = unknown = unpriced = 0
        cost = Decimal(0)
        priced = False
        for turn in self.session.turns:
            if turn.usage is None:
                unknown += 1
                continue
            values = turn.usage
            input_tokens += values["input_tokens"]
            output_tokens += values["output_tokens"]
            total += values["total_tokens"]
            estimate = self.settings.model_info(turn.model_id).cost(
                values["input_tokens"], values["output_tokens"]
            )
            if estimate is None:
                unpriced += 1
            else:
                cost += estimate
                priced = True
        message = tr(
            "会话累计（已知）Token：输入 {input} / 输出 {output} / 总计 {total}",
            input=input_tokens,
            output=output_tokens,
            total=total,
        )
        if unknown:
            message += tr("；{count} 次用量未知", count=unknown)
        if priced:
            message += tr("；已定价部分预估 US${cost:.8f}", cost=cost)
            if unpriced:
                message += tr("（{count} 次未定价）", count=unpriced)
        self.say(message, style="dim")

    def _show(self, limit: int) -> None:
        if not self.session.turns:
            self.say(tr("本会话还没有对话。"))
        for number, turn in enumerate(self.session.turns, 1):
            if number <= len(self.session.turns) - limit:
                continue
            self.say(
                f"#{number} · {turn.timestamp} · {turn.model_id} · {turn.status}", style="cyan"
            )
            self.say(turn.user or tr("（仅图片）"))
            for i, image in enumerate(turn.attachments, 1):
                self.say(
                    tr(
                        "  图片 #{index}：{name} · {width}×{height}",
                        index=i,
                        name=image.name,
                        width=image.width,
                        height=image.height,
                    )
                    + " · "
                    + tr("已保存" if image.file else "未保存")
                )
            if turn.assistant:
                self.console.print(_markdown(turn.assistant))

    @property
    def provider_name(self) -> str:
        return "AWS Bedrock" if self.settings.provider == "bedrock" else "OpenAI-compatible"

    def _config(self) -> None:
        s = self.settings
        self.say(tr("后端：{provider}", provider=self.provider_name))
        if s.provider == "bedrock":
            self.say(
                tr(
                    "Region：{region}；Profile：{profile}",
                    region=s.region or tr("由 boto3 解析"),
                    profile=s.profile or tr("boto3 默认凭证链"),
                )
            )
        else:
            self.say(
                tr(
                    "端点：{endpoint}；请求流式输出：{stream}",
                    endpoint=s.endpoint,
                    stream=tr("开启" if s.openai_stream else "关闭"),
                )
            )
        self.say(
            tr(
                "模型：{model}；输入预算（估算）：{input}；输出上限：{output}",
                model=self.session.model_id or tr("未设置"),
                input=s.max_input_tokens,
                output=s.max_output_tokens,
            )
        )
        self.say(
            tr(
                "历史：{history}；图片保存：{images}；图片长边/字节上限：{edge} px / {size} B",
                history=tr("开启" if s.history_enabled else "关闭"),
                images=tr("开启" if s.save_images else "关闭"),
                edge=s.max_edge,
                size=s.max_bytes,
            )
        )
        self.say(tr("界面语言：{language}", language=LANGUAGES[self.language]))

    def _set_session_state(self, **values) -> None:
        previous = {key: getattr(self.session, key) for key in values}
        for key, value in values.items():
            setattr(self.session, key, value)
        try:
            self.store.save_state(self.session)
        except HistoryError:
            for key, value in previous.items():
                setattr(self.session, key, value)
            raise

    def _remember_model(self, model_id: str) -> None:
        try:
            self.store.save_last_model(model_id)
        except (HistoryError, OSError):
            self.say(tr("无法保存下次启动的模型，请检查本地历史目录权限。"), style="yellow")

    def _switch_session(self, session) -> None:
        self.session = session
        self.attachments.clear()
        self.draft = ""
        self.say(tr("会话：{id} · {name}；/show 查看记录。", id=session.id[:12], name=session.name))

    def _local(self, command: str, args: list[str]) -> bool:
        match command, args:
            case "/exit", []:
                return False
            case "/new", names:
                new = self.store.new(" ".join(names) or tr("未命名"), self.session.model_id)
                self.store.save_state(new)
                self._switch_session(new)
            case "/history" | "/search", words:
                sessions = self.store.list(" ".join(words))
                for session in sessions:
                    mark = "*" if session.id == self.session.id else " "
                    self.say(f"{mark} {session.id[:12]} · {session.name}")
                if not sessions:
                    self.say(tr("没有匹配的本地会话。"))
            case "/resume" | "/switch", [ident]:
                self._switch_session(self.store.load(ident))
            case "/show", []:
                self._show(5)
            case "/show", [limit]:
                self._show(_positive(limit))
            case "/rename", [ident, name, *rest]:
                renamed = self.store.rename(ident, " ".join([name, *rest]))
                if self.session.id == renamed.id:
                    self.session.name = renamed.name
                self.say(tr("已重命名：{name}", name=renamed.name))
            case "/delete", [ident]:
                target = self.store.load(ident)
                self.store.delete(target.id)
                if self.session.id == target.id:
                    self._switch_session(
                        self.store.new(tr("未命名"), model_id=self.session.model_id)
                    )
                self.say(tr("已删除：{id}", id=target.id[:12]))
            case "/model", []:
                self.say(tr("当前模型：{model}", model=self.session.model_id or tr("未设置")))
                for alias, info in self.settings.models.items():
                    vision = {True: "支持图片", False: "仅文本", None: "视觉未标注"}[info.vision]
                    self.say(f"  {alias} · {info.id} · {tr(vision)}")
            case "/model", [model]:
                model_id = self.settings.resolve_model(model)
                self._set_session_state(model_id=model_id)
                self._remember_model(model_id)
                self.say(tr("已切换：{model}", model=model_id))
            case "/clear", []:
                self._set_session_state(context_start=len(self.session.turns))
                self.say(tr("已清空发送上下文；原始历史与累计用量保持不变。"))
            case "/tokens", []:
                self._tokens()
            case "/image", options:
                self._image(options)
            case "/export", [] | [_]:
                path = Path(
                    args[0] if args else f"bedrock-chat-{self.session.id[:12]}.md"
                ).expanduser()
                self.say(tr("已导出：{path}", path=self.store.export(self.session, path)))
            case "/config", []:
                self._config()
            case "/lang", []:
                self.say(tr("界面语言：{language}", language=LANGUAGES[self.language]))
                for code, name in LANGUAGES.items():
                    self.say(f"  /lang {code} · {name}")
            case "/lang", [language] if language.lower() in LANGUAGES:
                self.language = language.lower()
                with using_language(self.language):
                    try:
                        self.preferences.save_language(self.language)
                    except (HistoryError, OSError):
                        self.say(tr("无法保存界面语言，下次启动时可能不会记住。"), style="yellow")
                    self.say(tr("界面语言已切换为：{language}", language=LANGUAGES[self.language]))
            case "/lang", _:
                self.say(tr("用法：/lang [zh|en|ja]"), style="yellow")
            case "/help", []:
                for syntax, meaning in COMMANDS.items():
                    self.say(f"{tr(syntax)}  {tr(meaning)}")
                for hint in (
                    "Enter 发送 · Esc 后按 Enter 换行 · Ctrl+G 粘贴图片 · Ctrl+C 中断 · Ctrl+R 搜索输入历史。",
                    "光标在图片标记后按 Backspace，或在标记前按 Delete，可移除该图片。",
                    "普通文字以斜杠开头时可用 // 转义；多行输入总被当作消息。",
                ):
                    self.say(tr(hint))
            case _:
                raise ValueError(tr("未知命令或参数不正确。用 /help 查看。"))
        return True

    def _send(self, text: str) -> None:
        model = self.session.model_id
        if not model:
            raise ChatError(tr("请先用 /model <ID> 设置模型，或在启动时提供 --model。"))
        if self.images and self.settings.model_info(model).vision is False:
            raise ImageError(tr("当前模型不支持图片；请切换模型，或先 /image drop all。"))
        s = self.settings
        previous = [
            (turn.user, turn.assistant)
            for turn in self.session.turns[self.session.context_start :]
            if turn.status == "complete" and turn.user.strip() and turn.assistant.strip()
        ]
        budget = build_request(
            model,
            previous,
            text,
            [(image.payload(), image.width, image.height) for image in self.images],
            max_input_tokens=s.max_input_tokens,
            max_output_tokens=s.max_output_tokens,
            system_prompt=s.system_prompt,
        )
        self.say(
            tr("发送中 · 输入估算 {tokens} Token", tokens=budget.estimated_tokens)
            + (
                tr(" · 略去 {turns} 个最早轮次", turns=budget.dropped_turns)
                if budget.dropped_turns
                else ""
            )
            + tr(" · Ctrl+C 中断"),
            style="dim",
        )
        preview = Preview(self.console)
        with preview.run():
            result = self.service.send(budget.request, preview.feed)
        status = (
            "interrupted"
            if result.stop_reason == "interrupted"
            else "error"
            if result.error
            else "complete"
        )
        if result.text:
            self.console.print(_markdown(result.text))
        if result.error:
            self.say(result.error, style="yellow")
        elif result.stop_reason not in ("end_turn", "stop_sequence"):
            self.say(tr("停止原因：{reason}", reason=result.stop_reason), style="dim")
        usage = asdict(result.usage) if result.usage is not None else None
        turn = Turn(
            model,
            text,
            result.text,
            [
                Attachment(image.name, image.format, image.width, image.height, len(image.data))
                for image in self.images
            ],
            usage,
            status,
        )
        try:
            self.store.save_turn(self.session, turn, [image.data for image in self.images])
        except HistoryError as error:
            if not any(saved is turn for saved in self.session.turns):
                self.session.turns.append(turn)
            self.say(tr("本轮未能写入持久历史：{error}", error=error), style="yellow")
        if status == "complete":
            self.attachments.clear()
            self._remember_model(model)
        self.draft = "".join(self.attachments)
        if usage is None:
            self.say(tr("本次 Token：未知（服务未返回用量；不代表零费用）。"), style="dim")
        else:
            estimate = s.model_info(model).cost(
                result.usage.input_tokens, result.usage.output_tokens
            )
            suffix = tr("；预估 US${cost:.8f}", cost=estimate) if estimate is not None else ""
            self.say(
                tr(
                    "本次 Token：输入 {input} / 输出 {output} / 总计 {total}",
                    input=result.usage.input_tokens,
                    output=result.usage.output_tokens,
                    total=result.usage.total_tokens,
                )
                + suffix,
                style="dim",
            )
        self._tokens()

    def dispatch(self, text: str) -> bool:
        with using_language(self.language):
            return self._dispatch(text)

    def _dispatch(self, text: str) -> bool:
        self.notice = ""
        self.draft = "".join(self.attachments)
        rendered_draft = text if markers(text) else self.draft + text
        text = plain_text(text)
        is_command = text.startswith("/") and not text.startswith("//") and "\n" not in text
        try:
            if is_command:
                try:
                    command, *args = shlex.split(text)
                except ValueError:
                    raise ValueError(tr("命令参数的引号或转义不完整。")) from None
                return self._local(command.lower(), args)
            if text.strip() or self.images:
                self.draft = rendered_draft
                self._send(text[1:] if text.startswith("//") else text)
        except (ChatError, ImageError, HistoryError, ValueError) as error:
            self.say(str(error), style="yellow")
        return True


def create_prompt(chat: Chat, **kwargs) -> PromptSession:
    keys = KeyBindings()

    @keys.add("enter")
    def accept(event):
        event.current_buffer.validate_and_handle()

    @keys.add("escape", "enter")
    def newline(event):
        event.current_buffer.insert_text("\n")

    @keys.add("c-g", filter=has_focus(DEFAULT_BUFFER))
    def attach(event):
        with using_language(chat.language):
            try:
                marker = chat.paste()
                event.current_buffer.insert_text(marker)
            except (ImageError, KeyboardInterrupt) as error:
                chat.notice = str(error) or tr("已取消图片粘贴。")
        event.app.invalidate()

    @keys.add(Keys.BracketedPaste)
    def text_paste(event):
        text = event.data.replace("\r\n", "\n").replace("\r", "\n")
        event.current_buffer.insert_text(plain_text(terminal_text(text)))

    @keys.add("c-x", "c-d", filter=has_focus(DEFAULT_BUFFER))
    def drop_last(event):
        if chat.attachments:
            marker = next(reversed(chat.attachments))
            buffer = event.current_buffer
            before = buffer.document.text_before_cursor.replace(marker, "")
            after = buffer.document.text_after_cursor.replace(marker, "")
            buffer.document = Document(before + after, len(before))
            with using_language(chat.language):
                chat.notice = tr("已移除最后一张图片。")
        event.app.invalidate()

    def label():
        with using_language(chat.language):
            return tr("你 › ")

    def chip_label(index: int) -> str:
        with using_language(chat.language):
            return tr("[图片 #{number}]", number=index)

    def sync_draft(buffer: Buffer) -> None:
        count = len(chat.attachments)
        chat.sync_attachments(buffer.text)
        if len(chat.attachments) < count:
            chat.notice = ""
        # Undo may restore the buffer character, but not the discarded image.
        stale = [mark for mark in markers(buffer.text) if mark not in chat.attachments]
        if stale:
            before = buffer.document.text_before_cursor
            after = buffer.document.text_after_cursor
            for mark in stale:
                before = before.replace(mark, "")
                after = after.replace(mark, "")
            buffer.document = Document(before + after, len(before))

    prompt = PromptSession(
        label,
        multiline=True,
        prompt_continuation="  … ",
        key_bindings=keys,
        history=TextHistory(),
        input_processors=[ImageChips(chat.attachments, chip_label)],
        completer=WordCompleter(COMMAND_NAMES, sentence=True),
        bottom_toolbar=chat.toolbar,
        complete_while_typing=False,
        enable_system_prompt=False,
        enable_open_in_editor=False,
        **kwargs,
    )
    prompt.default_buffer.on_text_changed += sync_draft
    return prompt


def _startup_config(argv: list[str]) -> tuple[str, dict, ConfigError | None]:
    # Read only the options needed to localize --help and startup diagnostics.
    bootstrap = argparse.ArgumentParser(add_help=False, allow_abbrev=False, exit_on_error=False)
    bootstrap.add_argument("--config", type=Path)
    bootstrap.add_argument("--lang")
    bootstrap.add_argument("--no-history", action="store_true")
    try:
        options, _ = bootstrap.parse_known_args(argv)
    except argparse.ArgumentError:
        return DEFAULT_LANGUAGE, {}, None  # The full parser reports invalid arguments.

    saved = None
    raw = {}
    explicit = options.lang if options.lang in LANGUAGES else None
    try:
        with using_language(explicit or DEFAULT_LANGUAGE):
            raw = read_config(options)
        history = raw.get("history", {})
        if not explicit and not options.no_history and isinstance(history, dict):
            store = HistoryStore(
                xdg_path("XDG_STATE_HOME", ".local/state"),
                enabled=history.get("enabled", True) is True,
            )
            saved = store.load_language()
        language = explicit or saved or configured_language(raw)
    except ConfigError as error:
        return explicit or saved or DEFAULT_LANGUAGE, raw, error
    return language, raw, None


def main(argv: list[str] | None = None) -> int:
    arguments = sys.argv[1:] if argv is None else argv
    language, raw, config_error = _startup_config(arguments)
    with using_language(language):
        args = parse_args(arguments)
        if config_error is not None:
            _startup_error(config_error)
            return 1
        return _run(args, raw)


def _startup_error(error: Exception) -> None:
    Console(stderr=True).print(
        terminal_text(str(error)), markup=False, highlight=False, style="red"
    )


def _run(args: argparse.Namespace, raw: dict) -> int:
    console = Console()
    try:
        settings = load_settings(args, raw=raw)
        if args.check_clipboard:
            image = paste_image(max_edge=settings.max_edge, max_bytes=settings.max_bytes)
            console.print(
                tr(
                    "剪贴板：{size} · {format}；未联网、未保存。",
                    size=describe_image(image),
                    format=image.format,
                ),
                markup=False,
                highlight=False,
            )
            return 0
        if not sys.stdin.isatty() or not sys.stdout.isatty():
            raise ConfigError(tr("请在交互式 WSL2 终端运行；可用 --help 查看选项。"))
        chat = Chat(settings, console)
        chat.say(tr("TermTuck · /help 查看命令 · Ctrl+G 粘贴截图 · Esc+Enter 换行"))
        chat.say(tr("后端：{provider}", provider=chat.provider_name), style="dim")
        if chat.session.model_id:
            chat.say(tr("当前模型：{model}", model=chat.session.model_id), style="dim")
        else:
            chat.say(tr("未配置模型；用 /model <ID> 设置，或通过 /resume 恢复已有会话。"))
        prompt = create_prompt(chat)
        while True:
            try:
                text = prompt.prompt(default=chat.draft)
                if not chat.dispatch(text):
                    return 0
            except KeyboardInterrupt:
                chat.draft = "".join(chat.attachments)
                with using_language(chat.language):
                    chat.say(tr("已取消当前操作；/exit 退出。"))
            except EOFError:
                return 0
    except (ConfigError, HistoryError, ImageError, OSError) as error:
        _startup_error(error)
        return 1
    except KeyboardInterrupt:
        return 130


if __name__ == "__main__":
    raise SystemExit(main())
