import random
import subprocess
import sys
import time
from base64 import b64decode
from io import BytesIO
from struct import pack
from zlib import crc32

import pytest
from PIL import Image, PngImagePlugin

from bedrock_chat import images
from bedrock_chat.images import ImageAttachment, ImageError, read_image


def test_read_png_resizes_strips_metadata_and_returns_bedrock_payload(tmp_path):
    path = tmp_path / "截图.png"
    metadata = PngImagePlugin.PngInfo()
    metadata.add_text("Comment", "private source path")
    Image.new("RGBA", (320, 160), (0, 128, 255, 100)).save(path, pnginfo=metadata)

    result = read_image(path, max_edge=100)

    assert isinstance(result, ImageAttachment)
    assert result.name == "截图.png"
    assert (result.width, result.height) == (100, 50)
    assert result.payload() == {"format": "png", "source": {"bytes": result.data}}
    with Image.open(BytesIO(result.data)) as decoded:
        assert decoded.format == "PNG"
        assert decoded.size == (100, 50)
        assert decoded.getpixel((0, 0))[3] == 100
        assert "Comment" not in decoded.info


def test_read_jpeg_applies_exif_before_stripping_metadata(tmp_path):
    path = tmp_path / "oriented.jpg"
    source = Image.new("RGB", (80, 40), "blue")
    source.paste("red", (0, 0, 40, 40))
    exif = Image.Exif()
    exif[274] = 6  # Rotate 90 degrees clockwise.
    exif[270] = "private description"
    source.save(path, exif=exif)

    result = read_image(path)

    assert result.format == "jpeg"
    assert (result.width, result.height) == (40, 80)
    with Image.open(BytesIO(result.data)) as decoded:
        assert decoded.format == "JPEG"
        assert not decoded.getexif()
        assert decoded.getpixel((20, 10))[0] > 240
        assert decoded.getpixel((20, 70))[2] > 240


def test_read_uses_decoded_format_and_safe_basename(tmp_path):
    path = tmp_path / "C:\\private\\photo\x1b\n.jpg"
    Image.new("RGB", (8, 6)).save(path, format="PNG")

    result = read_image(path)

    assert result.format == "png"
    assert result.name == "photo.png"


def test_read_keeps_png_under_byte_limit_by_downscaling(tmp_path):
    path = tmp_path / "noise.png"
    Image.frombytes("RGB", (500, 250), random.Random(42).randbytes(500 * 250 * 3)).save(path)

    result = read_image(path, max_bytes=12_000)

    assert result.format == "png"
    assert len(result.data) <= 12_000
    assert 64 <= result.width < 500
    assert result.width / result.height == pytest.approx(2, abs=0.04)
    with Image.open(BytesIO(result.data)) as decoded:
        assert decoded.size == (result.width, result.height)


@pytest.mark.parametrize("kind", ["missing", "GIF", "animated PNG", "truncated", "garbage"])
def test_read_rejects_invalid_inputs_without_leaking_path(tmp_path, kind):
    path = tmp_path / "private.png"
    if kind == "GIF":
        Image.new("RGB", (8, 6)).save(path, format="GIF")
    elif kind == "animated PNG":
        Image.new("RGB", (8, 6)).save(
            path, save_all=True, append_images=[Image.new("RGB", (8, 6), "red")]
        )
    elif kind == "truncated":
        Image.new("RGB", (8, 6)).save(path)
        path.write_bytes(path.read_bytes()[:-12])  # Even a missing IEND is invalid.
    elif kind == "garbage":
        path.write_bytes(b"not an image")

    with pytest.raises(ImageError) as caught:
        read_image(path)
    assert str(tmp_path) not in str(caught.value)


def test_read_checks_source_file_size(tmp_path, monkeypatch):
    path = tmp_path / "padded.png"
    Image.new("RGB", (8, 6)).save(path)
    monkeypatch.setattr(images, "_MAX_INPUT_BYTES", 200)
    with path.open("ab") as file:
        file.write(b"\0" * 200)

    with pytest.raises(ImageError, match="文件.*大"):
        read_image(path)


def test_read_checks_pixel_budget_before_decode(tmp_path, monkeypatch):
    path = tmp_path / "large.png"
    Image.new("RGB", (8, 6)).save(path)
    data = path.read_bytes()
    header = b"IHDR" + pack(">II", 7000, 7000) + data[24:29]
    path.write_bytes(data[:12] + header + pack(">I", crc32(header)) + data[33:])

    def no_decode(*args, **kwargs):
        pytest.fail("oversized image should not be decoded")

    monkeypatch.setattr(Image.Image, "load", no_decode)
    with pytest.raises(ImageError, match="像素"):
        read_image(path)


@pytest.mark.parametrize("limits", [{"max_bytes": 3_750_001}, {"max_edge": 0}, {"max_bytes": 0}])
def test_read_rejects_out_of_range_limits(tmp_path, limits):
    path = tmp_path / "valid.png"
    Image.new("RGB", (8, 6)).save(path)

    with pytest.raises(ImageError):
        read_image(path, **limits)


def test_read_rejects_impossibly_small_byte_budget(tmp_path):
    path = tmp_path / "valid.png"
    Image.new("RGB", (8, 6)).save(path)

    with pytest.raises(ImageError):
        read_image(path, max_bytes=1)


@pytest.fixture
def mock_interop(monkeypatch):
    popen = subprocess.Popen

    def install(stdout=b"", *, status=0, delay=0):
        commands = []

        def spawn(command, **kwargs):
            commands.append(command)
            assert kwargs["stdin"] == subprocess.DEVNULL
            assert kwargs["stderr"] == subprocess.DEVNULL
            assert kwargs["stdout"] == subprocess.PIPE
            assert not kwargs.get("shell", False)
            program = (
                f"import sys, time; sys.stdout.buffer.write({stdout!r}); sys.stdout.flush(); "
                f"time.sleep({delay}); sys.exit({status})"
            )
            return popen([sys.executable, "-c", program], **kwargs)

        monkeypatch.setattr(subprocess, "Popen", spawn)
        return commands

    return install


def test_paste_uses_static_sta_command_and_returns_clean_png(mock_interop):
    metadata = PngImagePlugin.PngInfo()
    metadata.add_text("Comment", "clipboard metadata")
    with BytesIO() as output:
        Image.new("RGB", (180, 120), "red").save(output, format="PNG", pnginfo=metadata)
        commands = mock_interop(output.getvalue())

    result = images.paste_image(max_edge=90, max_bytes=2000)

    assert len(commands) == 1
    command = commands[0]
    assert command[0] == "powershell.exe"
    assert "-NoProfile" in command and "-STA" in command
    script = b64decode(command[command.index("-EncodedCommand") + 1], validate=True)
    script = script.decode("utf-16-le")
    assert "System.Windows.Forms" in script
    assert "OpenStandardOutput" in script
    assert script.index("'PNG'") < script.index("DataFormats]::Bitmap")
    assert "GetText" not in script and "Get-Clipboard" not in script
    assert result.name == "clipboard.png" and result.format == "png"
    assert (result.width, result.height) == (90, 60)
    with Image.open(BytesIO(result.data)) as decoded:
        assert decoded.size == (90, 60)
        assert decoded.getpixel((0, 0)) == (255, 0, 0)
        assert "Comment" not in decoded.info


@pytest.mark.parametrize(
    ("status", "stdout", "message"),
    [
        (0, b"", "没有图片"),
        (0, b"invalid PNG", "无法解码"),
        (10, b"", "没有图片"),
        (11, b"", "读取剪贴板"),
        (12, b"", "剪贴板图片太大"),
        (1, b"secret process output", "读取剪贴板"),
    ],
)
def test_paste_reports_clipboard_errors_without_output(mock_interop, status, stdout, message):
    mock_interop(stdout, status=status)
    with pytest.raises(ImageError, match=message) as caught:
        images.paste_image()
    assert "secret process output" not in str(caught.value)


def test_paste_rejects_jpeg_on_png_transport(mock_interop):
    with BytesIO() as output:
        Image.new("RGB", (8, 6)).save(output, format="JPEG")
        mock_interop(output.getvalue())

    with pytest.raises(ImageError, match="无法解码"):
        images.paste_image()


def test_paste_bounds_stdout_before_decoder_or_process_exit(mock_interop, monkeypatch):
    monkeypatch.setattr(images, "_MAX_INPUT_BYTES", 128)
    monkeypatch.setattr(images, "_CLIPBOARD_TIMEOUT", 2)
    mock_interop(b"x" * 4096, delay=10)

    with pytest.raises(ImageError, match="剪贴板图片太大"):
        images.paste_image()


def test_paste_times_out_waiting_for_stdout(mock_interop, monkeypatch):
    monkeypatch.setattr(images, "_CLIPBOARD_TIMEOUT", 0.2)
    mock_interop(delay=10)

    start = time.monotonic()
    with pytest.raises(ImageError, match="超时"):
        images.paste_image()
    assert time.monotonic() - start < 5


def test_paste_reports_unavailable_wsl_interop(monkeypatch):
    def unavailable(*args, **kwargs):
        raise FileNotFoundError("not public")

    monkeypatch.setattr(subprocess, "Popen", unavailable)
    with pytest.raises(ImageError, match="WSL"):
        images.paste_image()
