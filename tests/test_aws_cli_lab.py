"""Offline integration: real AWS profiles and SigV4, synthetic AWS responses."""

import hashlib
import json
import os
import struct
import subprocess
import zlib
from contextlib import closing
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest
from botocore.awsrequest import AWSResponse
from botocore.credentials import SSOProvider
from botocore.httpsession import URLLib3Session
from botocore.tokens import SSOTokenProvider

from bedrock_chat.bedrock import Bedrock, Usage, build_request

MODEL = "fixture.model"
REGION = "us-east-1"
PROFILE = "offline-fixture"
KEY_ID = "offline-test-key"
ROLE_KEY = "offline-role-key"
PROMPT = "仅供本地测试"


class RawResponse:
    def __init__(self, body):
        self.body = body

    def stream(self, amt=None, decode_content=False):
        if amt is None:
            yield self.body
        else:
            for offset in range(0, len(self.body), amt):
                yield self.body[offset : offset + amt]

    def close(self):
        pass


def aws_event(event_type, payload):
    def header(name, value):
        name, value = name.encode(), value.encode()
        return struct.pack("!B", len(name)) + name + struct.pack("!BH", 7, len(value)) + value

    headers = (
        header(":message-type", "event")
        + header(":event-type", event_type)
        + header(":content-type", "application/json")
    )
    body = json.dumps(payload).encode()
    prelude = struct.pack("!II", 16 + len(headers) + len(body), len(headers))
    frame = prelude + struct.pack("!I", zlib.crc32(prelude)) + headers + body
    return frame + struct.pack("!I", zlib.crc32(frame))


def request():
    return build_request(MODEL, [], PROMPT, [], max_input_tokens=512, max_output_tokens=32).request


def _role_credentials_response():
    return json.dumps(
        {
            "roleCredentials": {
                "accessKeyId": ROLE_KEY,
                "secretAccessKey": "offline-role-secret",
                "sessionToken": "offline-role-session",
                "expiration": int(
                    (datetime.now(timezone.utc) + timedelta(hours=1)).timestamp() * 1000
                ),
            }
        }
    ).encode()


@pytest.fixture
def lab(tmp_path, monkeypatch):
    for key in os.environ.copy():
        if key.startswith("AWS_") or key.endswith("_PROXY") or key.endswith("_proxy"):
            monkeypatch.delenv(key)
    cli = os.environ.get("BEDROCK_TEST_AWS_CLI")
    home = tmp_path / "home"
    config = home / ".aws" / "config"
    creds = config.with_name("credentials")
    cache = config.parent / "sso" / "cache"
    cache.mkdir(mode=0o700, parents=True)
    monkeypatch.setenv("HOME", str(home))
    monkeypatch.setenv("AWS_CONFIG_FILE", str(config))
    monkeypatch.setenv("AWS_SHARED_CREDENTIALS_FILE", str(creds))
    monkeypatch.setenv("AWS_EC2_METADATA_DISABLED", "true")
    monkeypatch.setenv("BOTO_CONFIG", str(tmp_path / "no-boto-config"))
    monkeypatch.setattr(SSOProvider, "_SSO_TOKEN_CACHE_DIR", str(cache))
    monkeypatch.setattr(SSOTokenProvider, "_SSO_TOKEN_CACHE_DIR", str(cache))

    received = []

    def send(_self, req):
        received.append(req)
        if req.url.startswith(f"https://portal.sso.{REGION}.amazonaws.com/federation/credentials?"):
            return AWSResponse(req.url, 200, {}, RawResponse(_role_credentials_response()))
        if (
            req.url
            == f"https://bedrock-runtime.{REGION}.amazonaws.com/model/{MODEL}/converse-stream"
        ):
            body = b"".join(
                (
                    aws_event(
                        "contentBlockDelta", {"contentBlockIndex": 0, "delta": {"text": "本地回复"}}
                    ),
                    aws_event("messageStop", {"stopReason": "end_turn"}),
                    aws_event(
                        "metadata",
                        {
                            "usage": {"inputTokens": 10, "outputTokens": 5, "totalTokens": 15},
                            "metrics": {"latencyMs": 1},
                        },
                    ),
                )
            )
            return AWSResponse(
                req.url,
                200,
                {"content-type": "application/vnd.amazon.eventstream"},
                RawResponse(body),
            )
        raise AssertionError("Unexpected endpoint in offline test")

    monkeypatch.setattr(URLLib3Session, "send", send)
    return home, config, cache, cli, received


def test_official_cli_writes_profile_used_by_bedrock(lab, monkeypatch):
    home, _, _, cli, received = lab
    if not cli:
        pytest.skip("set BEDROCK_TEST_AWS_CLI to an official AWS CLI v2 executable")
    cli = str(Path(cli).resolve(strict=True))
    env = {
        key: os.environ[key]
        for key in (
            "HOME",
            "AWS_CONFIG_FILE",
            "AWS_SHARED_CREDENTIALS_FILE",
            "AWS_EC2_METADATA_DISABLED",
        )
    }
    env.update(AWS_PAGER="", AWS_CLI_AUTO_PROMPT="off", PATH=os.defpath)

    def configure(*args):
        return subprocess.run(
            [cli, "configure", *args],
            cwd=home,
            check=True,
            env=env,
            capture_output=True,
            text=True,
            timeout=10,
        ).stdout.strip()

    for key, value in (
        ("region", REGION),
        ("aws_access_key_id", KEY_ID),
        ("aws_secret_access_key", "offline-profile-secret"),
        ("aws_session_token", "offline-profile-session"),
    ):
        configure("set", key, value, "--profile", PROFILE)
    assert PROFILE in configure("list-profiles").splitlines()
    assert configure("get", "region", "--profile", PROFILE) == REGION

    result = Bedrock(profile=PROFILE).send(request(), lambda _: None)
    monkeypatch.setenv("AWS_PROFILE", PROFILE)
    env_result = Bedrock().send(request(), lambda _: None)
    assert result == env_result
    assert (result.error, result.text, result.usage) == (None, "本地回复", Usage(10, 5, 15))
    assert len(received) == 2
    for signed in received:
        assert f"Credential={KEY_ID}/".encode() in signed.headers["Authorization"]
        assert signed.headers["X-Amz-Security-Token"] == b"offline-profile-session"
        assert json.loads(signed.body)["messages"][0]["content"] == [{"text": PROMPT}]


@pytest.mark.parametrize("expiry_hours", [None, -1, 1], ids=["missing", "expired", "valid"])
def test_real_sdk_sso_provider_with_local_cache(lab, expiry_hours):
    _, config, cache, _, received = lab
    config.write_text(
        f"[profile {PROFILE}]\nregion = {REGION}\nsso_session = offline\n"
        "sso_account_id = 123456789012\nsso_role_name = Fixture\n"
        f"[sso-session offline]\nsso_region = {REGION}\n"
        "sso_start_url = https://example.invalid/start\nsso_registration_scopes = sso:account:access\n",
        encoding="utf-8",
    )
    if expiry_hours is not None:
        expiry = datetime.now(timezone.utc) + timedelta(hours=expiry_hours)
        cache_key = hashlib.sha1(b"offline").hexdigest()
        (cache / f"{cache_key}.json").write_text(
            json.dumps(
                {
                    "accessToken": "offline-sso-test",
                    "expiresAt": expiry.isoformat(),
                }
            ),
            encoding="utf-8",
        )

    result = Bedrock(profile=PROFILE).send(request(), lambda _: None)
    if expiry_hours != 1:
        assert result.error and "aws sso login" in result.error
        assert "offline-sso-test" not in result.error
        assert result.usage is None and received == []
    else:
        assert (result.error, result.text, result.usage) == (None, "本地回复", Usage(10, 5, 15))
        sso_call, bedrock_call = received
        assert sso_call.headers["x-amz-sso_bearer_token"] == b"offline-sso-test"
        assert f"Credential={ROLE_KEY}/".encode() in bedrock_call.headers["Authorization"]
        assert bedrock_call.headers["X-Amz-Security-Token"] == b"offline-role-session"


def test_region_precedence_matches_aws_cli_v2(lab, monkeypatch):
    _, config, _, _, _ = lab
    config.write_text("[default]\nregion = us-east-1\n", encoding="utf-8")
    monkeypatch.setenv("AWS_DEFAULT_REGION", "us-east-2")
    monkeypatch.setenv("AWS_REGION", "us-west-2")
    with closing(Bedrock()._make_client()) as inferred:
        assert inferred.meta.region_name == "us-west-2"
    with closing(Bedrock(region="us-west-1")._make_client()) as explicit:
        assert explicit.meta.region_name == "us-west-1"
