"""Offline coverage of budgeting and the Bedrock streaming boundary."""

from contextlib import closing
from unittest.mock import Mock

import boto3
import pytest
from botocore import exceptions as aws
from botocore.httpsession import URLLib3Session
from botocore.stub import Stubber
from urllib3.exceptions import ReadTimeoutError

from bedrock_chat.bedrock import Bedrock, ChatError, Usage, build_request

PNG = {"format": "png", "source": {"bytes": b"synthetic-fixture"}}
MODEL = "example.model"
STOP = {"messageStop": {"stopReason": "end_turn"}}
METADATA = {"metadata": {"usage": {"inputTokens": 11, "outputTokens": 22, "totalTokens": 33}}}


def build(previous=None, text="hello", images=None, *, limit=6000, system=""):
    return build_request(
        MODEL,
        previous or [],
        text,
        images or [],
        max_input_tokens=limit,
        max_output_tokens=64,
        system_prompt=system,
    )


def delta(**value):
    return {"contentBlockDelta": {"delta": value, "contentBlockIndex": 0}}


class Stream:
    def __init__(self, *events):
        self.events = events
        self.close = Mock()

    def __iter__(self):
        for event in self.events:
            if isinstance(event, BaseException):
                raise event
            yield event


@pytest.fixture(autouse=True)
def forbid_aws_http(monkeypatch):
    def unexpected_request(*args, **kwargs):
        pytest.fail("Offline tests must not make an AWS HTTP request")

    monkeypatch.setattr(URLLib3Session, "send", unexpected_request)


@pytest.fixture
def session_factory(monkeypatch):
    factory = Mock(spec=boto3.Session)
    monkeypatch.setattr(boto3, "Session", factory)
    return factory


def test_build_request_shape_with_text_only_history_and_images():
    budget = build([("hi", "你好")], "question", [(PNG, 720, 480)], system="Be concise")
    assert budget.request == {
        "modelId": MODEL,
        "system": [{"text": "Be concise"}],
        "inferenceConfig": {"maxTokens": 64},
        "messages": [
            {"role": "user", "content": [{"text": "hi"}]},
            {"role": "assistant", "content": [{"text": "你好"}]},
            {"role": "user", "content": [{"image": PNG}, {"text": "question"}]},
        ],
    }
    assert budget.estimated_tokens == 508
    assert budget.dropped_turns == 0


def test_evicts_oldest_complete_pairs_until_the_budget_fits():
    previous = [("old prompt", "old answer"), ("new prompt", "new answer")]
    recent = build(previous[1:])
    budget = build(previous, limit=recent.estimated_tokens)
    assert budget.request == recent.request
    assert budget.estimated_tokens == recent.estimated_tokens
    assert budget.dropped_turns == 1
    assert previous[0] == ("old prompt", "old answer")
    current = build()
    budget = build(previous, limit=current.estimated_tokens)
    assert budget.request == current.request
    assert budget.dropped_turns == 2


@pytest.mark.parametrize("text", ["", " \n"])
def test_image_only_message_has_no_empty_text_block(text):
    budget = build(text=text, images=[(PNG, 1024, 512)])
    assert budget.estimated_tokens == build(text="", images=[(PNG, 1024, 512)]).estimated_tokens
    assert budget.request["messages"] == [{"role": "user", "content": [{"image": PNG}]}]
    assert budget.estimated_tokens > build(text="", images=[(PNG, 512, 512)]).estimated_tokens
    assert "system" not in budget.request


def test_cjk_estimate_is_larger_than_ascii_with_the_same_character_count():
    assert build(text="中日英かな").estimated_tokens > build(text="abcde").estimated_tokens


@pytest.mark.parametrize(
    "kwargs",
    [
        {"text": "字" * 100},
        {"images": [(PNG, 8192, 8192)]},
        {"system": "system " * 100},
    ],
)
def test_oversized_current_input_is_rejected_before_network(kwargs, session_factory):
    with pytest.raises(ChatError, match="预算"):
        build(limit=64, **kwargs)
    session_factory.assert_not_called()


@pytest.mark.parametrize("resource", ["prompt/abc:1", "prompt-version/abc/1"])
def test_prompt_management_arn_is_rejected_at_both_entry_points(resource, session_factory):
    arn = f"arn:aws:bedrock:us-east-1:123456789012:{resource}"
    with pytest.raises(ChatError, match="Prompt Management"):
        build_request(arn, [], "hello", [], max_input_tokens=64, max_output_tokens=64)
    result = Bedrock().send({**build().request, "modelId": arn}, Mock())
    assert result.error and "Prompt Management" in result.error
    session_factory.assert_not_called()


@pytest.mark.parametrize(
    "extra",
    [
        {"toolConfig": {}},
        {"additionalModelRequestFields": {"thinking": {"type": "enabled"}}},
        {"messages": [{"role": "assistant", "content": [{"reasoningContent": {}}]}]},
        {
            "messages": [
                {"role": "user", "content": [{"image": PNG}]},
                {"role": "assistant", "content": [{"text": "hello"}]},
                {"role": "user", "content": [{"text": "next"}]},
            ]
        },
    ],
)
def test_send_rejects_tools_thinking_and_history_images_locally(session_factory, extra):
    result = Bedrock().send({**build().request, **extra}, Mock())
    assert result.stop_reason == "error" and result.error
    session_factory.assert_not_called()


@pytest.mark.parametrize("region,profile", [(None, None), ("ap-northeast-1", "work-sso")])
def test_lazy_session_uses_profile_region_and_reuses_client(session_factory, region, profile):
    bedrock = Bedrock(region, profile)
    session_factory.assert_not_called()
    request = build().request
    client = session_factory.return_value.client.return_value
    client.converse_stream.side_effect = [{"stream": Stream(STOP)}, {"stream": Stream(STOP)}]
    for _ in range(2):
        assert bedrock.send(request, Mock()).error is None
    session_factory.assert_called_once()
    sdk_kwargs = session_factory.call_args.kwargs
    assert sdk_kwargs["region_name"] == region and sdk_kwargs["profile_name"] == profile
    session_client = session_factory.return_value.client
    session_client.assert_called_once()
    args, kwargs = session_client.call_args
    assert args == ("bedrock-runtime",)
    assert kwargs.keys() == {"config"}  # no explicit endpoint or TLS override
    config = kwargs["config"]
    assert config.retries["total_max_attempts"] == 1
    assert config.ignore_configured_endpoint_urls is True
    assert 0 < config.connect_timeout <= 5
    assert 0 < config.read_timeout <= 60
    assert client.converse_stream.call_count == 2


def test_streams_text_not_reasoning_or_tools_and_reads_metadata_after_stop(session_factory):
    stream = Stream(
        delta(reasoningContent={"text": "hidden", "signature": "ignored"}),
        delta(toolUse={"input": "ignored"}),
        delta(text="你"),
        delta(text="好"),
        STOP,
        METADATA,
    )
    client = session_factory.return_value.client.return_value
    client.converse_stream.return_value = {"stream": stream}
    request = build().request
    chunks = []
    result = Bedrock().send(request, chunks.append)
    assert chunks == ["你", "好"]
    assert (result.text, result.usage, result.stop_reason, result.error) == (
        "你好",
        Usage(11, 22, 33),
        "end_turn",
        None,
    )
    client.converse_stream.assert_called_once_with(**request)
    stream.close.assert_called_once_with()
    followup = build([("hello", result.text)])
    assert followup.request["messages"][1]["content"] == [{"text": "你好"}]


@pytest.mark.parametrize("metadata", [None, {"metadata": {"usage": {"inputTokens": 0}}}])
def test_missing_or_incomplete_metadata_is_unknown(session_factory, metadata):
    events = [{"messageStop": {"stopReason": "max_tokens"}}]
    if metadata:
        events.append(metadata)
    session_factory.return_value.client.return_value.converse_stream.return_value = {
        "stream": Stream(*events)
    }
    result = Bedrock().send(build().request, Mock())
    assert result.stop_reason == "max_tokens" and result.error is None
    assert result.usage is None


@pytest.mark.parametrize(
    "events,stop,keyword",
    [
        ([], "error", "结束"),
        ([{"messageStop": {}}], "error", "结束"),
        ([{"messageStop": {"stopReason": "tool_use"}}], "tool_use", "工具"),
        ([{"messageStop": {"stopReason": "content_filtered"}}], "content_filtered", "过滤"),
        ([{"messageStop": {"stopReason": "private-value"}}], "error", "结束"),
        ([{"throttlingException": {"message": "private-value"}}], "error", "限流"),
        (
            [
                aws.EventStreamError(
                    {"Error": {"Code": "ModelStreamErrorException"}}, "ConverseStream"
                )
            ],
            "error",
            "中断",
        ),
        ([ReadTimeoutError(None, None, "private-value")], "error", "超时"),
    ],
)
def test_failures_keep_partial_text_and_usage(session_factory, events, stop, keyword):
    stream = Stream(delta(text="partial"), METADATA, *events)
    client = session_factory.return_value.client.return_value
    client.converse_stream.return_value = {"stream": stream}
    result = Bedrock().send(build().request, Mock())
    assert (result.text, result.usage, result.stop_reason) == ("partial", Usage(11, 22, 33), stop)
    assert keyword in result.error and "private-value" not in result.error
    client.converse_stream.assert_called_once()
    stream.close.assert_called_once_with()


@pytest.mark.parametrize("during_callback", [False, True])
def test_ctrl_c_closes_stream_and_keeps_received_data(session_factory, during_callback):
    stream = Stream(METADATA, delta(text="partial"), KeyboardInterrupt())
    session_factory.return_value.client.return_value.converse_stream.return_value = {
        "stream": stream
    }
    callback = Mock(side_effect=KeyboardInterrupt if during_callback else None)
    result = Bedrock().send(build().request, callback)
    assert (result.text, result.usage, result.stop_reason) == (
        "partial",
        Usage(11, 22, 33),
        "interrupted",
    )
    assert result.error
    stream.close.assert_called_once_with()


def test_ctrl_c_during_client_creation_returns_interrupted(session_factory):
    session_factory.return_value.client.side_effect = KeyboardInterrupt
    result = Bedrock().send(build().request, Mock())
    assert result.stop_reason == "interrupted" and result.error


@pytest.mark.parametrize(
    "stage,exc,keyword",
    [
        ("session", aws.ProfileNotFound(profile="private-value"), "配置文件"),
        ("client", aws.NoRegionError(), "区域"),
        (
            "client",
            aws.TokenRetrievalError(provider="sso", error_msg="private-value"),
            "aws sso login",
        ),
        ("call", aws.SSOTokenLoadError(error_msg="private-value"), "aws sso login"),
        ("call", aws.UnauthorizedSSOTokenError(), "aws sso login"),
        ("call", aws.NoCredentialsError(), "凭证"),
        ("call", RuntimeError("private-value"), "失败"),
    ],
)
def test_safe_errors_at_each_stage(session_factory, caplog, stage, exc, keyword):
    caplog.set_level("DEBUG")
    session = session_factory.return_value
    target = {
        "session": session_factory,
        "client": session.client,
        "call": session.client.return_value.converse_stream,
    }[stage]
    target.side_effect = exc
    result = Bedrock().send(build().request, Mock())
    assert result.stop_reason == "error" and keyword in result.error
    assert "private-value" not in result.error
    assert "private-value" not in caplog.text
    target.assert_called_once()


@pytest.fixture
def isolated_aws(tmp_path, monkeypatch):
    monkeypatch.delenv("AWS_PROFILE", raising=False)
    monkeypatch.delenv("AWS_DEFAULT_PROFILE", raising=False)
    monkeypatch.setenv("AWS_SHARED_CREDENTIALS_FILE", str(tmp_path / "credentials"))
    monkeypatch.setenv("AWS_EC2_METADATA_DISABLED", "true")
    monkeypatch.setenv("AWS_ACCESS_KEY_ID", "fixture")
    monkeypatch.setenv("AWS_SECRET_ACCESS_KEY", "fixture")
    config = tmp_path / "config"
    config.write_text(
        "[default]\nregion = us-east-1\nservices = overrides\n"
        "endpoint_url = http://config.invalid\n"
        "[services overrides]\nbedrock-runtime =\n  endpoint_url = http://service-config.invalid\n"
    )
    monkeypatch.setenv("AWS_CONFIG_FILE", str(config))
    monkeypatch.delenv("AWS_DEFAULT_REGION", raising=False)
    return config


@pytest.mark.parametrize("via_env", [False, True])
def test_real_client_ignores_endpoint_overrides_and_resolves_default_region(
    isolated_aws,
    monkeypatch,
    via_env,
):
    for key in ("AWS_ENDPOINT_URL", "AWS_ENDPOINT_URL_BEDROCK_RUNTIME"):
        monkeypatch.delenv(key, raising=False)
        if via_env:
            monkeypatch.setenv(key, "http://environment.invalid")
    bedrock = Bedrock()
    with closing(bedrock._make_client()) as client:
        assert client.meta.region_name == "us-east-1"
        assert client.meta.endpoint_url == "https://bedrock-runtime.us-east-1.amazonaws.com"
        assert client.meta.config.retries["total_max_attempts"] == 1


def test_client_side_monitoring_stays_off_even_if_enabled_externally(isolated_aws, monkeypatch):
    publisher = Mock(side_effect=AssertionError("must not create a telemetry publisher"))
    monkeypatch.setattr("botocore.monitoring.SocketPublisher", publisher)
    monkeypatch.setenv("AWS_CSM_ENABLED", "true")
    with closing(Bedrock()._make_client()):
        publisher.assert_not_called()


@pytest.mark.parametrize(
    "code,with_images,keyword,vision_hint",
    [
        ("AccessDeniedException", False, "IAM", False),
        ("ExpiredTokenException", False, "aws sso login", False),
        ("ValidationException", True, "校验", True),
        ("ValidationException", False, "校验", False),
        ("private-value", False, "失败", False),
    ],
)
def test_real_stubber_errors_are_safe(isolated_aws, code, with_images, keyword, vision_hint):
    bedrock = Bedrock()
    request = build(images=[(PNG, 120, 120)] if with_images else []).request
    with closing(bedrock._make_client()) as client:
        bedrock._client = client
        with Stubber(client) as stub:
            stub.add_client_error(
                "converse_stream",
                code,
                "private-value with an invalid image",
                expected_params=request,
            )
            result = bedrock.send(request, Mock())
            stub.assert_no_pending_responses()
    assert result.stop_reason == "error"
    assert keyword in result.error and "private-value" not in result.error
    assert ("视觉" in result.error) == vision_hint
