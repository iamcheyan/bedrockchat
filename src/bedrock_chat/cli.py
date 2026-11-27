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
from prompt_toolkit.application.current import get_app
from prompt_toolkit.completion import WordCompleter
from prompt_toolkit.enums import DEFAULT_BUFFER
from prompt_toolkit.filters import Condition, has_focus
from prompt_toolkit.history import InMemoryHistory
from prompt_toolkit.key_binding import KeyBindings
from prompt_toolkit.keys import Keys
from rich.console import Console
from rich.live import Live
from rich.markdown import Markdown

from .bedrock import Bedrock, ChatError, build_request
from .config import ConfigError, Settings, load_settings, terminal_text
from .history import Attachment, HistoryError, HistoryStore, Turn
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
    "/help": "显示本帮助",
    "/exit": "退出",
}
COMMAND_NAMES = list(dict.fromkeys(command.split()[0] for command in COMMANDS))


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="termtuck", description="支持图片、本地历史和 Token 预算的 AWS Bedrock 终端聊天"
    )
    parser.add_argument(
        "--version", action="version", version=f"%(prog)s {version('bedrock-chat')}"
    )
    parser.add_argument("--config", type=Path, help="显式读取指定的 TOML 文件")
    parser.add_argument("--region", help="AWS Region")
    parser.add_argument("--profile", help="AWS Profile")
    parser.add_argument(
        "--model", dest="model_id", help="Bedrock 模型 ID、推理配置文件 ID/ARN 或别名"
    )
    parser.add_argument("--max-input-tokens", type=int, help="本地估算的输入 Token 预算")
    parser.add_argument("--max-output-tokens", type=int, help="Bedrock 请求的最大输出 Token")
    parser.add_argument("--no-history", action="store_true", help="不读写持久历史、图片或上次模型")
    parser.add_argument("--no-save-images", action="store_true", help="不保存图片字节到本地历史")
    parser.add_argument(
        "--check-clipboard", action="store_true", help="仅测试并显示剪贴板图片尺寸，不联网"
    )
    return parser.parse_args(argv)


def describe_image(image: ImageAttachment) -> str:
    return f"{image.width}×{image.height} · {len(image.data) / 1024:.1f} KiB"


def _positive(value: str) -> int:
    if not value.isascii() or not value.isdecimal() or len(value) > 9 or int(value) < 1:
        raise ValueError("请输入正整数序号。")
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
        self.service = (
            service if service is not None else Bedrock(settings.region, settings.profile)
        )
        self.store = HistoryStore(
            settings.history_dir, enabled=settings.history_enabled, save_images=settings.save_images
        )
        last_model = None if settings.model_explicit else self.store.load_last_model()
        self.session = self.store.new(model_id=last_model or settings.model_id)
        self.images: list[ImageAttachment] = []
        self.notice = ""
        self.draft = ""

    def say(self, message: str, *, style: str | None = None) -> None:
        self.console.print(terminal_text(message), markup=False, highlight=False, style=style)

    def toolbar(self) -> str:
        details = "  ".join(
            f"#{i} {describe_image(image)}" for i, image in enumerate(self.images, 1)
        )
        message = f"图片 {len(self.images)} 张" + (f"  {details}" if details else "")
        if self.notice:
            message += f"  │  {self.notice}"
        elif not details:
            message += "  │  Ctrl+G 粘贴截图 · Esc+Enter 换行 · Enter 发送"
        return terminal_text(message).replace("\n", " ").replace("\t", " ")

    def _check_attach(self) -> None:
        if (
            self.session.model_id
            and self.settings.model_info(self.session.model_id).vision is False
        ):
            raise ImageError("当前模型不支持图片，请用 /model 切换到视觉模型。")
        if len(self.images) >= MAX_IMAGES:
            raise ImageError(f"最多附加 {MAX_IMAGES} 张图片。")

    def _add_image(self, image: ImageAttachment) -> None:
        if sum(len(item.data) for item in self.images) + len(image.data) > MAX_IMAGE_BODY:
            raise ImageError("图片合计超过 15 MB，请移除部分图片或减少其尺寸。")
        self.images.append(image)
        self.notice = f"已附加 #{len(self.images)}（{describe_image(image)}）"

    def _image_options(self) -> dict[str, int]:
        return {"max_edge": self.settings.max_edge, "max_bytes": self.settings.max_bytes}

    def paste(self) -> None:
        self._check_attach()
        self._add_image(paste_image(**self._image_options()))

    def _image(self, args: list[str]) -> None:
        if not args or args == ["list"]:
            self.say(self.toolbar())
        elif args == ["paste"]:
            self.paste()
            self.say(self.notice)
        elif args[0] == "drop" and len(args) <= 2:
            if args[1:] == ["all"]:
                self.images.clear()
            else:
                index = _positive(args[1]) if len(args) == 2 else len(self.images)
                if not 1 <= index <= len(self.images):
                    raise ImageError("没有这个待发送图片序号。")
                self.images.pop(index - 1)
            self.say(self.toolbar())
        elif args[0] == "reuse" and len(args) == 3:
            self._check_attach()
            path = self.store.attachment_path(self.session, _positive(args[1]), _positive(args[2]))
            self._add_image(read_image(path, **self._image_options()))
            self.say(self.notice)
        elif len(args) == 1:
            self._check_attach()
            self._add_image(read_image(Path(args[0]).expanduser(), **self._image_options()))
            self.say(self.notice)
        else:
            raise ValueError("用法：/image <路径> | paste | drop [序号|all] | reuse <轮次> <序号>")

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
        message = (
            f"会话累计（已知）Token：输入 {input_tokens} / 输出 {output_tokens} / 总计 {total}"
        )
        if unknown:
            message += f"；{unknown} 次用量未知"
        if priced:
            message += f"；已定价部分预估 US${cost:.8f}"
            if unpriced:
                message += f"（{unpriced} 次未定价）"
        self.say(message, style="dim")

    def _show(self, limit: int) -> None:
        if not self.session.turns:
            self.say("本会话还没有对话。")
        for number, turn in enumerate(self.session.turns, 1):
            if number <= len(self.session.turns) - limit:
                continue
            self.say(
                f"#{number} · {turn.timestamp} · {turn.model_id} · {turn.status}", style="cyan"
            )
            self.say(turn.user or "（仅图片）")
            for i, image in enumerate(turn.attachments, 1):
                self.say(
                    f"  图片 #{i}：{image.name} · {image.width}×{image.height}"
                    + (" · 已保存" if image.file else " · 未保存")
                )
            if turn.assistant:
                self.console.print(_markdown(turn.assistant))

    def _config(self) -> None:
        s = self.settings
        self.say(
            f"Region：{s.region or '由 boto3 解析'}；Profile：{s.profile or 'boto3 默认凭证链'}\n"
            f"模型：{self.session.model_id or '未设置'}；输入预算（估算）：{s.max_input_tokens}；"
            f"输出上限：{s.max_output_tokens}\n"
            f"历史：{'开启' if s.history_enabled else '关闭'}；"
            f"图片保存：{'开启' if s.save_images else '关闭'}；"
            f"图片长边/字节上限：{s.max_edge} px / {s.max_bytes} B"
        )

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
            self.say("无法保存下次启动的模型，请检查本地历史目录权限。", style="yellow")

    def _switch_session(self, session) -> None:
        self.session = session
        self.images.clear()
        self.draft = ""
        self.say(f"会话：{session.id[:12]} · {session.name}；/show 查看记录。")

    def _local(self, command: str, args: list[str]) -> bool:
        match command, args:
            case "/exit", []:
                return False
            case "/new", names:
                new = self.store.new(" ".join(names) or "未命名", self.session.model_id)
                self.store.save_state(new)
                self._switch_session(new)
            case "/history" | "/search", words:
                sessions = self.store.list(" ".join(words))
                for session in sessions:
                    mark = "*" if session.id == self.session.id else " "
                    self.say(f"{mark} {session.id[:12]} · {session.name}")
                if not sessions:
                    self.say("没有匹配的本地会话。")
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
                self.say(f"已重命名：{renamed.name}")
            case "/delete", [ident]:
                target = self.store.load(ident)
                self.store.delete(target.id)
                if self.session.id == target.id:
                    self._switch_session(self.store.new(model_id=self.session.model_id))
                self.say(f"已删除：{target.id[:12]}")
            case "/model", []:
                self.say(f"当前模型：{self.session.model_id or '未设置'}")
                for alias, info in self.settings.models.items():
                    vision = {True: "支持图片", False: "仅文本", None: "视觉未标注"}[info.vision]
                    self.say(f"  {alias} · {info.id} · {vision}")
            case "/model", [model]:
                model_id = self.settings.resolve_model(model)
                self._set_session_state(model_id=model_id)
                self._remember_model(model_id)
                self.say(f"已切换：{model_id}")
            case "/clear", []:
                self._set_session_state(context_start=len(self.session.turns))
                self.say("已清空发送上下文；原始历史与累计用量保持不变。")
            case "/tokens", []:
                self._tokens()
            case "/image", options:
                self._image(options)
            case "/export", [] | [_]:
                path = Path(
                    args[0] if args else f"bedrock-chat-{self.session.id[:12]}.md"
                ).expanduser()
                self.say(f"已导出：{self.store.export(self.session, path)}")
            case "/config", []:
                self._config()
            case "/help", []:
                for syntax, meaning in COMMANDS.items():
                    self.say(f"{syntax}  {meaning}")
                self.say(
                    "Enter 发送 · Esc 后按 Enter 换行 · Ctrl+G 粘贴图片 · Ctrl+C 中断 · Ctrl+R 搜索输入历史。\n"
                    "输入框为空时按 Backspace 移除最后一张待发送图片。\n"
                    "普通文字以斜杠开头时可用 // 转义；多行输入总被当作消息。"
                )
            case _:
                raise ValueError("未知命令或参数不正确。用 /help 查看。")
        return True

    def _send(self, text: str) -> None:
        model = self.session.model_id
        if not model:
            raise ChatError("请先用 /model <ID> 设置模型，或在启动时提供 --model。")
        if self.images and self.settings.model_info(model).vision is False:
            raise ImageError("当前模型不支持图片；请切换模型，或先 /image drop all。")
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
            f"发送中 · 输入估算 {budget.estimated_tokens} Token"
            + (f" · 略去 {budget.dropped_turns} 个最早轮次" if budget.dropped_turns else "")
            + " · Ctrl+C 中断",
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
            self.say(f"停止原因：{result.stop_reason}", style="dim")
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
            self.say(f"本轮未能写入持久历史：{error}", style="yellow")
        if status == "complete":
            self.images.clear()
            self._remember_model(model)
        if usage is None:
            self.say("本次 Token：未知（未收到 Bedrock 用量事件；不代表零费用）。", style="dim")
        else:
            estimate = s.model_info(model).cost(
                result.usage.input_tokens, result.usage.output_tokens
            )
            suffix = f"；预估 US${estimate:.8f}" if estimate is not None else ""
            self.say(
                f"本次 Token：输入 {result.usage.input_tokens} / 输出 {result.usage.output_tokens}"
                f" / 总计 {result.usage.total_tokens}{suffix}",
                style="dim",
            )
        self._tokens()

    def dispatch(self, text: str) -> bool:
        self.notice = ""
        self.draft = ""
        is_command = text.startswith("/") and not text.startswith("//") and "\n" not in text
        try:
            if is_command:
                command, *args = shlex.split(text)
                return self._local(command.lower(), args)
            if text.strip() or self.images:
                self._send(text[1:] if text.startswith("//") else text)
        except (ChatError, ImageError, HistoryError, ValueError) as error:
            if not is_command:
                self.draft = text
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

    @keys.add("c-g")
    def attach(event):
        try:
            chat.paste()
        except (ImageError, KeyboardInterrupt) as error:
            chat.notice = str(error) or "已取消图片粘贴。"
        event.app.invalidate()

    @keys.add(Keys.BracketedPaste)
    def text_paste(event):
        text = event.data.replace("\r\n", "\n").replace("\r", "\n")
        event.current_buffer.insert_text(terminal_text(text))

    @Condition
    def can_backspace_image():
        return bool(chat.images) and not get_app().current_buffer.text

    @keys.add(Keys.Backspace, filter=has_focus(DEFAULT_BUFFER) & can_backspace_image)
    @keys.add("c-x", "c-d")
    def drop_last(event):
        if chat.images:
            chat.images.pop()
            chat.notice = "已移除最后一张图片。"
        event.app.invalidate()

    return PromptSession(
        "你 › ",
        multiline=True,
        prompt_continuation="  … ",
        key_bindings=keys,
        history=InMemoryHistory(),
        completer=WordCompleter(COMMAND_NAMES, sentence=True),
        bottom_toolbar=chat.toolbar,
        complete_while_typing=False,
        enable_system_prompt=False,
        enable_open_in_editor=False,
        **kwargs,
    )


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    console = Console()
    try:
        settings = load_settings(args)
        if args.check_clipboard:
            image = paste_image(max_edge=settings.max_edge, max_bytes=settings.max_bytes)
            console.print(
                f"剪贴板：{describe_image(image)} · {image.format}；未联网、未保存。",
                markup=False,
                highlight=False,
            )
            return 0
        if not sys.stdin.isatty() or not sys.stdout.isatty():
            raise ConfigError("请在交互式 WSL2 终端运行；可用 --help 查看选项。")
        chat = Chat(settings, console)
        chat.say("TermTuck · /help 查看命令 · Ctrl+G 粘贴截图 · Esc+Enter 换行")
        if chat.session.model_id:
            chat.say(f"当前模型：{chat.session.model_id}", style="dim")
        else:
            chat.say("未配置模型；用 /model <ID> 设置，或通过 /resume 恢复已有会话。")
        prompt = create_prompt(chat)
        while True:
            try:
                text = prompt.prompt(default=chat.draft)
                if not chat.dispatch(text):
                    return 0
            except KeyboardInterrupt:
                chat.draft = ""
                chat.say("已取消当前操作；/exit 退出。")
            except EOFError:
                return 0
    except (ConfigError, HistoryError, ImageError, OSError) as error:
        Console(stderr=True).print(
            terminal_text(str(error)), markup=False, highlight=False, style="red"
        )
        return 1
    except KeyboardInterrupt:
        return 130


if __name__ == "__main__":
    raise SystemExit(main())
