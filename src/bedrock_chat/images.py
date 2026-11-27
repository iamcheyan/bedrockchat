"""Prepare local images for the Bedrock Converse API."""

import os
import selectors
import stat
import subprocess
import unicodedata
from base64 import b64encode
from dataclasses import dataclass
from io import BytesIO
from math import sqrt
from pathlib import Path, PureWindowsPath
from struct import error as StructError
from time import monotonic

from PIL import Image, ImageOps

from .i18n import tr

_DEFAULT_MAX_BYTES = 1_500_000
_BEDROCK_MAX_BYTES = 3_750_000
_BEDROCK_MAX_EDGE = 8000
# The source may be much larger than the transmitted image (e.g. a 4K screenshot).
_MAX_INPUT_BYTES = 32 * 1024 * 1024
_MAX_INPUT_PIXELS = 40_000_000
_CLIPBOARD_TIMEOUT = 15

# Fixed script: no paths, settings, or other caller data are passed to PowerShell.
# Keep the transport and bitmap limits in sync with the source limits above.
_CLIPBOARD_SCRIPT = r"""
$ErrorActionPreference = 'Stop'
$maxBytes = 32MB
$maxPixels = 40000000
try {
    Add-Type -AssemblyName System.Windows.Forms
    Add-Type -AssemblyName System.Drawing
    $clipboard = [System.Windows.Forms.Clipboard]::GetDataObject()
    if ($null -eq $clipboard) { exit 10 }

    $stdout = [Console]::OpenStandardOutput()
    # Snipping Tool may provide PNG alongside, or instead of, a bitmap.
    foreach ($format in @('PNG', 'image/png')) {
        try { $png = $clipboard.GetData($format, $false) }
        catch { continue }
        if ($png -is [IO.MemoryStream]) {
            try {
                if ($png.Length -eq 0) { continue }
                if ($png.Length -gt $maxBytes) { exit 12 }
                $png.WriteTo($stdout)
                $stdout.Flush()
                exit 0
            } finally { $png.Dispose() }
        }
        if ($png -is [byte[]] -and $png.Length -gt 0) {
            if ($png.Length -gt $maxBytes) { exit 12 }
            $stdout.Write($png, 0, $png.Length)
            $stdout.Flush()
            exit 0
        }
    }

    $image = $clipboard.GetData([System.Windows.Forms.DataFormats]::Bitmap, $true)
    if ($image -isnot [System.Drawing.Image]) { exit 10 }
    try {
        if ([long]$image.Width * $image.Height -gt $maxPixels) { exit 12 }
        $buffer = New-Object IO.MemoryStream
        try {
            $image.Save($buffer, [System.Drawing.Imaging.ImageFormat]::Png)
            if ($buffer.Length -gt $maxBytes) { exit 12 }
            $buffer.WriteTo($stdout)
            $stdout.Flush()
        } finally { $buffer.Dispose() }
    } finally { $image.Dispose() }
} catch { exit 11 }
exit 0
"""
_CLIPBOARD_COMMAND = (
    "powershell.exe",
    "-NoLogo",
    "-NoProfile",
    "-NonInteractive",
    "-STA",
    "-EncodedCommand",
    b64encode(_CLIPBOARD_SCRIPT.encode("utf-16-le")).decode("ascii"),
)


class ImageError(Exception):
    """An image could not be attached."""


@dataclass(frozen=True)
class ImageAttachment:
    data: bytes
    format: str
    width: int
    height: int
    name: str

    def payload(self) -> dict:
        return {"format": self.format, "source": {"bytes": self.data}}


def _check_limits(max_edge: int, max_bytes: int) -> None:
    if type(max_edge) is not int or not 1 <= max_edge <= _BEDROCK_MAX_EDGE:
        raise ImageError(tr("图片边长限制必须为 1～{max_edge} 像素。", max_edge=_BEDROCK_MAX_EDGE))
    if type(max_bytes) is not int or not 1 <= max_bytes <= _BEDROCK_MAX_BYTES:
        raise ImageError(
            tr("图片大小限制必须为 1～{max_bytes} 字节。", max_bytes=_BEDROCK_MAX_BYTES)
        )


def _decode_image(data: bytes, formats: tuple[str, ...]) -> tuple[Image.Image, str]:
    try:
        with BytesIO(data) as buffer:
            with Image.open(buffer, formats=formats) as source:
                if not (0 < source.width * source.height <= _MAX_INPUT_PIXELS):
                    raise ImageError(tr("原始图片的像素数超出限制。"))
                if getattr(source, "n_frames", 1) != 1:
                    raise ImageError(tr("不支持多帧图片。"))
                image_format = source.format
                source.verify()
            # verify() invalidates the decoder; reopen to load the pixels.
            with Image.open(buffer, formats=formats) as source:
                ImageOps.exif_transpose(source, in_place=True)
                mode = "RGBA" if image_format == "PNG" and source.has_transparency_data else "RGB"
                image = source.convert(mode)
                image.info.clear()
            return image, "png" if image_format == "PNG" else "jpeg"
    except (Image.DecompressionBombError, Image.DecompressionBombWarning):
        raise ImageError(tr("原始图片的像素数超出限制。")) from None
    except (OSError, SyntaxError, ValueError, TypeError, IndexError, OverflowError, StructError):
        raise ImageError(tr("图片无法解码，请使用有效的 PNG 或 JPEG 图片。")) from None


def _fit_image(image: Image.Image, format: str, max_edge: int, max_bytes: int) -> bytes:
    # PNG keeps text and line art sharp. For JPEG, try a high quality encoding first.
    options = {"optimize": True}
    if format == "jpeg":
        options.update(quality=92, subsampling=0)
    image.thumbnail((max_edge, max_edge), Image.Resampling.LANCZOS)
    while True:
        try:
            with BytesIO() as output:
                image.save(output, format=format.upper(), **options)
                size = output.tell()
                if size <= max_bytes:
                    return output.getvalue()
        except (OSError, ValueError):
            raise ImageError(tr("无法编码图片。")) from None
        edge = max(image.size)
        if edge == 1:
            raise ImageError(tr("图片无法缩小到指定的字节限制。"))
        # Bytes roughly follow pixel count. Make measured reductions instead of
        # dropping to a fixed small thumbnail; compression may change after resizing.
        next_edge = max(1, int(edge * max(0.5, min(0.95, sqrt(max_bytes / size)))))
        image.thumbnail((next_edge, next_edge), Image.Resampling.LANCZOS)


def _safe_name(name: str, format: str) -> str:
    name = PureWindowsPath(name).name
    stem = "".join(c for c in Path(name).stem if not unicodedata.category(c).startswith("C"))
    stem = stem.strip(" .") or "image"
    suffix = ".jpg" if format == "jpeg" else ".png"
    return stem + suffix


def read_image(
    path: Path, *, max_edge: int = 1568, max_bytes: int = _DEFAULT_MAX_BYTES
) -> ImageAttachment:
    """Read a local PNG or JPEG and return a sanitized, size-limited attachment."""
    _check_limits(max_edge, max_bytes)
    try:
        # Nonblocking open also avoids hanging on a FIFO before checking its type.
        with open(path, "rb", opener=lambda name, flags: os.open(name, flags | os.O_NONBLOCK)) as f:
            info = os.fstat(f.fileno())
            if not stat.S_ISREG(info.st_mode):
                raise ImageError(tr("图片路径必须指向普通文件。"))
            if info.st_size > _MAX_INPUT_BYTES:
                raise ImageError(tr("原始图片文件太大。"))
            data = f.read(_MAX_INPUT_BYTES + 1)
    except (OSError, ValueError):
        raise ImageError(tr("无法读取图片文件。")) from None
    if len(data) > _MAX_INPUT_BYTES:
        raise ImageError(tr("原始图片文件太大。"))

    image, format = _decode_image(data, ("PNG", "JPEG"))
    with image:
        encoded = _fit_image(image, format, max_edge, max_bytes)
        return ImageAttachment(encoded, format, *image.size, _safe_name(path.name, format))


def _read_clipboard() -> bytes:
    deadline = monotonic() + _CLIPBOARD_TIMEOUT
    data = bytearray()
    try:
        with subprocess.Popen(
            _CLIPBOARD_COMMAND,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
        ) as process:
            try:
                # WSL is Linux: select on the pipe so even a partial write cannot
                # block past the deadline. Never collect arbitrary-sized stdout.
                assert process.stdout is not None
                with selectors.DefaultSelector() as selector:
                    selector.register(process.stdout, selectors.EVENT_READ)
                    while True:
                        remaining = deadline - monotonic()
                        if remaining <= 0 or not selector.select(remaining):
                            raise TimeoutError
                        read_size = min(65536, _MAX_INPUT_BYTES + 1 - len(data))
                        chunk = os.read(process.stdout.fileno(), read_size)
                        if not chunk:
                            break
                        data.extend(chunk)
                        if len(data) > _MAX_INPUT_BYTES:
                            raise ImageError(tr("剪贴板图片太大。"))
                status = process.wait(timeout=max(0, deadline - monotonic()))
            finally:
                if process.poll() is None:
                    process.kill()
    except (TimeoutError, subprocess.TimeoutExpired):
        raise ImageError(tr("读取 Windows 剪贴板超时，请重试。")) from None
    except OSError:
        raise ImageError(tr("无法启动 Windows PowerShell，请检查 WSL 互操作是否可用。")) from None
    if status == 12:
        raise ImageError(tr("剪贴板图片太大。"))
    if status == 10 or (status == 0 and not data):
        raise ImageError(tr("Windows 剪贴板中没有图片。"))
    if status != 0:
        raise ImageError(tr("无法读取剪贴板，请重试。"))
    return bytes(data)


def paste_image(*, max_edge: int = 1568, max_bytes: int = _DEFAULT_MAX_BYTES) -> ImageAttachment:
    """Explicitly read a Windows clipboard image, independently of terminal paste."""
    _check_limits(max_edge, max_bytes)
    image, format = _decode_image(_read_clipboard(), ("PNG",))
    with image:
        encoded = _fit_image(image, format, max_edge, max_bytes)
        return ImageAttachment(encoded, format, *image.size, "clipboard.png")
