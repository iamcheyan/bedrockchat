"""Offline regressions for localized Bedrock and image diagnostics."""

from contextlib import nullcontext
from unittest.mock import MagicMock, Mock

import pytest
from botocore.exceptions import ClientError

from bedrock_chat.bedrock import Bedrock, ChatError, build_request
from bedrock_chat.i18n import using_language
from bedrock_chat.images import ImageError, read_image


def test_budget_error_formats_estimate_and_limit_in_selected_language():
    for language, expected in (
        (
            "en",
            "The current message (including images and the system prompt) exceeds the input "
            "token budget: estimated 12, limit 11.",
        ),
        (None, "当前消息（含图片和系统提示词）超出输入 Token 预算：估算 12，上限 11。"),
    ):
        context = using_language(language) if language else nullcontext()
        with context, pytest.raises(ChatError) as caught:
            build_request("example.model", [], "hey", [], max_input_tokens=11, max_output_tokens=16)
        assert str(caught.value) == expected


def test_image_limit_error_formats_limit_in_japanese(tmp_path):
    with using_language("ja"), pytest.raises(ImageError) as caught:
        read_image(tmp_path / "unused.png", max_edge=0)
    assert str(caught.value) == "画像の辺の長さの上限は 1～8000 ピクセルにしてください。"


@pytest.mark.parametrize(
    "event,stop,english,chinese",
    [
        (
            {"throttlingException": {"message": "private AWS details"}},
            "error",
            "Bedrock throttled the request. Try again later.",
            "Bedrock 请求被限流，请稍后再试。",
        ),
        (
            {"messageStop": {"stopReason": "content_filtered"}},
            "content_filtered",
            "The Bedrock content filter blocked the response.",
            "Bedrock 内容过滤器阻止了回复。",
        ),
    ],
)
def test_stream_errors_follow_language_on_same_client(monkeypatch, event, stop, english, chinese):
    bedrock = Bedrock()
    client = Mock()
    monkeypatch.setattr(bedrock, "_make_client", lambda: client)
    client.converse_stream.return_value = {"stream": MagicMock(__iter__=lambda _: iter([event]))}
    request = build_request(
        "example.model", [], "中文 and English", [], max_input_tokens=64, max_output_tokens=16
    ).request

    with using_language("en"):
        localized = bedrock.send(request, Mock())
    default = bedrock.send(request, Mock())

    assert (localized.stop_reason, localized.error) == (stop, english)
    assert (default.stop_reason, default.error) == (stop, chinese)
    assert [call.kwargs for call in client.converse_stream.call_args_list] == [request, request]


@pytest.mark.parametrize("with_image", [False, True])
def test_validation_error_translates_visual_hint_only_for_images(monkeypatch, with_image):
    bedrock = Bedrock()
    client = Mock()
    monkeypatch.setattr(bedrock, "_make_client", lambda: client)
    client.converse_stream.side_effect = ClientError(
        {"Error": {"Code": "ValidationException", "Message": "private validation details"}},
        "ConverseStream",
    )
    images = [({"format": "png", "source": {"bytes": b"fixture"}}, 1, 1)] if with_image else []
    request = build_request(
        "example.model", [], "hey", images, max_input_tokens=64, max_output_tokens=16
    ).request

    with using_language("en"):
        result = bedrock.send(request, Mock())

    expected = "Bedrock rejected the request. Check the model ID, region, and input parameters."
    if with_image:
        expected += " Make sure the model supports image input and the images meet its limits."
    assert result.error == expected
