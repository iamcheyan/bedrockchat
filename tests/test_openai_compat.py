"""OpenAI-compatible transport tests using only an ephemeral loopback server."""

import json
from base64 import b64encode
from http.server import BaseHTTPRequestHandler, HTTPServer
from threading import Event, Thread
from types import SimpleNamespace

import pytest

from bedrock_chat import openai_compat
from bedrock_chat.bedrock import SendResult, Usage, build_request
from bedrock_chat.i18n import using_language
from bedrock_chat.openai_compat import OpenAICompatible

PATH = "/custom/v1/chat/completions"
USAGE = {"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}


def request(*, previous=(), text="问", images=(), system=""):
    return build_request(
        "vision:local",
        list(previous),
        text,
        list(images),
        max_input_tokens=6000,
        max_output_tokens=64,
        system_prompt=system,
    ).request


def completion(text="answer", stop="stop", usage=None):
    return {"choices": [{"message": {"content": text}, "finish_reason": stop}], "usage": usage}


@pytest.fixture
def server():
    records = []

    def respond(handler):
        reply(handler, json.dumps(completion()).encode())

    mock = SimpleNamespace(records=records, respond=respond)

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            body = self.rfile.read(int(self.headers["Content-Length"]))
            records.append((self.command, self.path, self.headers, json.loads(body)))
            mock.respond(self)

        def do_GET(self):
            records.append((self.command, self.path, self.headers, None))
            mock.respond(self)

        def log_message(self, *args):
            pass

    with HTTPServer(("127.0.0.1", 0), Handler) as httpd:
        mock.url = f"http://127.0.0.1:{httpd.server_port}{PATH}"
        worker = Thread(target=httpd.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True)
        worker.start()
        try:
            yield mock
        finally:
            httpd.shutdown()
            worker.join()


def reply(handler, body, content_type="application/json", status=200, headers=()):
    handler.send_response(status)
    handler.send_header("Content-Type", content_type)
    handler.send_header("Content-Length", str(len(body)))
    for name, value in headers:
        handler.send_header(name, value)
    handler.end_headers()
    handler.wfile.write(body)


def test_posts_built_text_request_as_chat_completions(server):
    transport = OpenAICompatible(server.url, stream=False)
    assert not server.records
    chunks = []
    result = transport.send(
        request(previous=[("前の質問", "前の答え")], system="Be concise"), chunks.append
    )

    [(method, path, headers, body)] = server.records
    assert (method, path) == ("POST", PATH)
    assert headers["Content-Type"] == "application/json"
    assert "Authorization" not in headers
    assert body == {
        "model": "vision:local",
        "messages": [
            {"role": "system", "content": "Be concise"},
            {"role": "user", "content": "前の質問"},
            {"role": "assistant", "content": "前の答え"},
            {"role": "user", "content": "问"},
        ],
        "max_tokens": 64,
        "stream": False,
    }
    assert chunks == ["answer"]
    assert result == SendResult("answer", None, "end_turn", None)


def sse(event):
    return f"data: {json.dumps(event, ensure_ascii=False)}\r\n\r\n".encode()


def delta(**fields):
    return {"choices": [{"delta": fields, "finish_reason": None}], "usage": None}


def finish(reason):
    return {"choices": [{"delta": {}, "finish_reason": reason}]}


DONE = b"data: [DONE]\n\n"
USAGE_EVENT = sse({"choices": [], "usage": USAGE})


def test_streams_text_before_completion_and_reads_usage_after_stop(server):
    got_first_chunk = Event()
    chunks = []
    first_arrived_before_stop = []

    def respond(handler):
        handler.protocol_version = "HTTP/1.1"
        handler.send_response(200)
        handler.send_header("Content-Type", "Text/Event-Stream; charset=utf-8")
        handler.send_header("Transfer-Encoding", "chunked")
        handler.end_headers()

        def write_chunk(data):
            handler.wfile.write(f"{len(data):x}\r\n".encode() + data + b"\r\n")
            handler.wfile.flush()

        first = (
            b"\xef\xbb\xbf: keepalive\n\n"
            + sse(delta(role="assistant", reasoning_content="private-reasoning"))
            + ('data: {"choices": [\ndata:{"delta": {"content": "你"}}]}\n\n').encode()
        )
        cut = first.index("你".encode()) + 1  # Split a UTF-8 character across HTTP chunks.
        write_chunk(first[:cut])
        write_chunk(first[cut:])
        first_arrived_before_stop.append(got_first_chunk.wait(2))
        write_chunk(sse(delta(content="好")) + sse(finish("length")) + USAGE_EVENT + DONE)
        handler.wfile.write(b"0\r\n\r\n")

    def on_text(text):
        chunks.append(text)
        got_first_chunk.set()

    server.respond = respond
    result = OpenAICompatible(server.url).send(request(), on_text)

    assert first_arrived_before_stop == [True]
    assert chunks == ["你", "好"]
    assert result == SendResult("你好", Usage(2, 3, 5), "max_tokens", None)
    [(_, _, _, body)] = server.records
    assert body["stream"] is True
    assert body["stream_options"] == {"include_usage": True}


@pytest.mark.parametrize(
    "tail", [b"", sse(finish("stop")), DONE, sse(finish("private-reason")) + DONE]
)
def test_incomplete_stream_preserves_partial_text_and_usage(server, tail):
    body = sse(delta(content="partial")) + USAGE_EVENT + tail
    server.respond = lambda handler: reply(handler, body, "text/event-stream")
    chunks = []
    result = OpenAICompatible(server.url).send(request(), chunks.append)

    assert chunks == ["partial"]
    assert (result.text, result.usage, result.stop_reason) == ("partial", Usage(2, 3, 5), "error")
    assert result.error and "未正常结束" in result.error and "private" not in result.error
    assert len(server.records) == 1


@pytest.mark.parametrize("text", ["describe", ""])
def test_png_and_jpeg_are_inline_data_uris_in_current_message(server, text):
    png, jpeg = b"\x89PNG\xff", b"\xff\xd8\xff"
    images = [
        ({"format": fmt, "source": {"bytes": data}}, 32, 32)
        for fmt, data in [("png", png), ("jpeg", jpeg)]
    ]
    result = OpenAICompatible(server.url).send(
        request(previous=[("old", "answer")], text=text, images=images), lambda _: None
    )

    assert result.error is None
    [(_, _, _, body)] = server.records
    expected = [
        {
            "type": "image_url",
            "image_url": {"url": f"data:image/{fmt};base64,{b64encode(data).decode()}"},
        }
        for fmt, data in [("png", png), ("jpeg", jpeg)]
    ]
    if text:
        expected.append({"type": "text", "text": text})
    assert body["messages"] == [
        {"role": "user", "content": "old"},
        {"role": "assistant", "content": "answer"},
        {"role": "user", "content": expected},
    ]


@pytest.mark.parametrize("change", ["tools", "role", "previous-image", "gif"])
def test_rejects_unsupported_input_without_sending(server, change):
    data = request(previous=[("old", "answer")])
    if change == "tools":
        data["toolConfig"] = {"tools": []}
    elif change == "role":
        data["messages"][1]["role"] = "tool"
    else:
        index, fmt = (0, "png") if change == "previous-image" else (-1, "gif")
        data["messages"][index]["content"] = [{"image": {"format": fmt, "source": {"bytes": b"x"}}}]
    result = OpenAICompatible(server.url).send(data, lambda _: pytest.fail("Unexpected text"))
    assert result.stop_reason == "error" and result.error
    assert not server.records


@pytest.mark.parametrize("stream", [True, False])
@pytest.mark.parametrize(
    "usage, expected", [(USAGE, Usage(2, 3, 5)), ({**USAGE, "total_tokens": True}, None)]
)
def test_json_reply_returns_usage_if_complete(server, stream, usage, expected):
    server.respond = lambda handler: reply(
        handler, json.dumps(completion("日本語", "stop", usage), ensure_ascii=False).encode()
    )
    chunks = []
    result = OpenAICompatible(server.url, stream=stream).send(request(), chunks.append)
    assert result == SendResult("日本語", expected, "end_turn", None)
    assert chunks == ["日本語"] and len(server.records) == 1


@pytest.mark.parametrize("stream", [True, False])
@pytest.mark.parametrize(
    "reason, expected, keyword",
    [
        ("tool_calls", "tool_use", "工具"),
        ("function_call", "tool_use", "工具"),
        ("content_filter", "content_filtered", "过滤"),
    ],
)
def test_maps_unsupported_finish_reasons_to_existing_status(
    server, stream, reason, expected, keyword
):
    if stream:
        body = sse(delta(tool_calls=[{"private": "do not display"}])) + sse(finish(reason)) + DONE
        server.respond = lambda handler: reply(handler, body, "text/event-stream")
    else:
        body = json.dumps(completion(None, reason)).encode()
        server.respond = lambda handler: reply(handler, body)
    result = OpenAICompatible(server.url, stream=stream).send(
        request(), lambda _: pytest.fail("Unexpected text")
    )
    assert result.text == "" and result.stop_reason == expected
    assert result.error and keyword in result.error


def test_invalid_json_choice_is_not_a_success(server):
    body = b'{"choices":[{"finish_reason":"stop","private":"private-response"}]}'
    server.respond = lambda handler: reply(handler, body)
    result = OpenAICompatible(server.url).send(request(), lambda _: pytest.fail("Unexpected text"))
    assert result.stop_reason == "error" and result.error and "private-response" not in result.error


@pytest.mark.parametrize("status", [302, 307, 400, 429, 500])
def test_http_failure_does_not_redirect_retry_or_show_error_body(server, caplog, status):
    def respond(handler):
        if handler.path != PATH:  # A redirect target that records any unexpected request.
            reply(handler, b"{}")
        else:
            reply(handler, b"private-response", status=status, headers=[("Location", "/other")])

    server.respond = respond
    result = OpenAICompatible(server.url).send(request(), lambda _: pytest.fail("Unexpected text"))
    assert result.stop_reason == "error" and result.error and f"HTTP {status}" in result.error
    assert "private-response" not in result.error and "private-response" not in caplog.text
    assert [(method, path) for method, path, _, _ in server.records] == [("POST", PATH)]


@pytest.mark.parametrize(
    "language, expected",
    [
        ("zh", "OpenAI 兼容请求失败（HTTP 429）。"),
        ("en", "The OpenAI-compatible request failed (HTTP 429)."),
        ("ja", "OpenAI 互換リクエストに失敗しました（HTTP 429）。"),
    ],
)
def test_http_failure_uses_interface_language(server, language, expected):
    server.respond = lambda handler: reply(handler, b"private-response", status=429)
    with using_language(language):
        result = OpenAICompatible(server.url).send(request(), lambda _: None)
    assert result == SendResult("", None, "error", expected)


def test_ignores_environment_proxy(server, monkeypatch):
    # Even a broken proxy implementation stays inside the isolated server.
    proxy = server.url.split(PATH)[0]
    for name in ("http_proxy", "HTTP_PROXY", "all_proxy", "ALL_PROXY"):
        monkeypatch.setenv(name, proxy)
    for name in ("no_proxy", "NO_PROXY"):
        monkeypatch.delenv(name, raising=False)
    result = OpenAICompatible(server.url).send(request(), lambda _: None)
    assert result.error is None
    assert [(method, path) for method, path, _, _ in server.records] == [("POST", PATH)]


def test_sse_error_keeps_partial_text_without_echoing_error(server):
    body = sse(delta(content="partial")) + sse({"error": {"message": "private-response"}})
    server.respond = lambda handler: reply(handler, body, "text/event-stream")
    result = OpenAICompatible(server.url).send(request(), lambda _: None)
    assert result.text == "partial" and result.stop_reason == "error"
    assert result.error and "private-response" not in result.error and len(server.records) == 1


@pytest.mark.parametrize("content_type", ["application/json", "text/event-stream"])
@pytest.mark.parametrize("declare_size", [True, False])
def test_bounds_response_size(server, monkeypatch, content_type, declare_size):
    monkeypatch.setattr(openai_compat, "_MAX_RESPONSE_BYTES", 150)

    def respond(handler):
        body = b" " * 200 if content_type == "application/json" else b": keepalive\n" * 20
        if declare_size:
            reply(handler, body, content_type)
        else:
            handler.send_response(200)
            handler.send_header("Content-Type", content_type)
            handler.end_headers()
            handler.wfile.write(body)

    server.respond = respond
    result = OpenAICompatible(server.url).send(request(), lambda _: pytest.fail("Unexpected text"))
    assert result.stop_reason == "error" and result.error and "大小限制" in result.error


def test_stalled_stream_times_out_and_closes_connection(server, monkeypatch):
    monkeypatch.setattr(openai_compat, "_IO_TIMEOUT", 0.15)
    disconnected = Event()

    def respond(handler):
        handler.connection.settimeout(2)
        handler.send_response(200)
        handler.send_header("Content-Type", "text/event-stream")
        handler.end_headers()
        handler.wfile.write(sse(delta(content="partial")))
        if handler.rfile.read(1) == b"":
            disconnected.set()

    server.respond = respond
    result = OpenAICompatible(server.url).send(request(), lambda _: None)
    assert disconnected.wait(2)
    assert result.text == "partial" and result.stop_reason == "error"
    assert result.error and "超时" in result.error and len(server.records) == 1


def test_keyboard_interrupt_preserves_partial_result_and_closes_connection(server):
    disconnected = Event()

    def respond(handler):
        handler.connection.settimeout(2)
        handler.send_response(200)
        handler.send_header("Content-Type", "text/event-stream")
        handler.end_headers()
        handler.wfile.write(USAGE_EVENT + sse(delta(content="partial")))
        if handler.rfile.read(1) == b"":
            disconnected.set()

    def interrupt(_):
        raise KeyboardInterrupt

    server.respond = respond
    result = OpenAICompatible(server.url).send(request(), interrupt)
    assert disconnected.wait(2)
    assert result == SendResult("partial", Usage(2, 3, 5), "interrupted", "已中断。")
