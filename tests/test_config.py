import argparse
from decimal import Decimal

import pytest

from bedrock_chat.config import ConfigError, load_settings, terminal_text


def test_default_config_does_not_read_current_directory(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "xdg"))
    (tmp_path / "config.toml").write_text("invalid\n")
    settings = load_settings(argparse.Namespace())
    assert settings.model_id is None
    assert settings.system_prompt == ""
    assert settings.history_enabled and not settings.save_images


def test_config_parses_model_metadata_and_cli_overrides(tmp_path):
    config = tmp_path / "config.toml"
    config.write_text("""[aws]
profile = "work"
region = "us-east-1"
model_id = "vision"
[chat]
max_input_tokens = 314
[models.vision]
id = "example.vision"
vision = true
input_per_million = 0.25
output_per_million = 2.0
""")
    settings = load_settings(argparse.Namespace(config=config, region="us-west-2", no_history=True))
    assert settings.model_id == "example.vision"
    assert settings.region == "us-west-2" and settings.profile == "work"
    assert settings.max_input_tokens == 314
    assert not settings.history_enabled
    assert settings.models["vision"].vision is True
    assert settings.model_info(settings.model_id).cost(1_000_000, 500_000) == Decimal("1.25")


@pytest.mark.parametrize(
    "content",
    [
        '[aws]\napi_key = "DO_NOT_PRINT"',
        "[chat]\nmax_input_tokens = true",
        "[history]\nsave_images = 1",
        "[images]\nmax_bytes = 3999999",
        '[models.bad]\nid = "a"\ninput_per_million = 1',
        '[models.bad]\nid = "a"\ninput_per_million = inf\noutput_per_million = 1',
        '[aws]\nregion = "us-east-1.evil.example"',
    ],
)
def test_invalid_or_secret_setting_is_rejected_without_value(tmp_path, content):
    path = tmp_path / "bad.toml"
    path.write_text(content)
    with pytest.raises(ConfigError) as raised:
        load_settings(argparse.Namespace(config=path))
    assert "DO_NOT_PRINT" not in str(raised.value)


def test_explicit_missing_config_is_an_error(tmp_path):
    with pytest.raises(ConfigError, match="配置文件"):
        load_settings(argparse.Namespace(config=tmp_path / "no.toml"))


def test_terminal_text_strips_escape_and_control_codes():
    assert terminal_text("日\x1b[31m中\x07\x9b1m\r\n\tEnglish") == "日[31m中1m\n\tEnglish"
