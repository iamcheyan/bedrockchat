# TermTuck

[简体中文](README.md) · English · [日本語](README.ja.md)

TermTuck is a terminal chat app for AWS Bedrock. It streams replies with Markdown and code highlighting, accepts image attachments, and keeps local chat history separate from the text sent back to the model. When the estimated input exceeds the configured budget, it drops the oldest complete turns first. It never automatically resends historical images.

## Install

TermTuck is designed for **Windows Terminal + WSL2**. In Windows PowerShell, run `wsl -l -v` and check that your distribution shows version `2`. See [Microsoft’s WSL installation guide](https://learn.microsoft.com/en-us/windows/wsl/install) if needed. Open the distribution in Windows Terminal and run the commands below **inside WSL**. From a native or remote Linux terminal, attach images by file path; the Windows screenshot shortcut needs a local Windows clipboard and WSL interop.

Install Python 3.11 or later, Git, and either pipx or uv. On the repository page, copy the URL under **Code → HTTPS**, clone it with `git clone`, then run from the cloned directory:

```bash
pipx install .
pipx ensurepath   # Reopen the terminal if termtuck was not on PATH.
# Or: uv tool install .
# If needed: uv tool update-shell

termtuck --help
```

TermTuck is not on PyPI; install from the checkout or a wheel built from it. The `bedrock-chat` command still works.

## Connect to AWS Bedrock

Install [AWS CLI v2](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html) in the environment where you run TermTuck. A Windows AWS CLI login is not automatically available in WSL. If your organization uses IAM Identity Center, configure and verify an example profile:

```bash
aws configure sso --profile work-sso
aws sso login --profile work-sso
aws sts get-caller-identity --profile work-sso

termtuck --profile work-sso --region us-east-1 --model amazon.nova-lite-v1:0 --lang en
```

Replace the example profile, Region, and model with ones your account can use. The model must support Bedrock `ConverseStream`, and IAM must allow `bedrock:InvokeModelWithResponseStream` on the resources actually invoked. Some models require an inference profile ID or ARN rather than a base model ID. If SSO expires, run `aws sso login --profile work-sso` again before sending. With an existing AWS profile, pass `--profile`; if no profile is set, TermTuck uses the boto3 default credential chain, including `AWS_PROFILE`.

To keep settings, copy [config.example.toml](config.example.toml) to `$XDG_CONFIG_HOME/bedrock-chat/config.toml` (default `~/.config/bedrock-chat/config.toml`) without overwriting an existing file, or pass `--config PATH`. Set `profile`, `region`, and `model_id` under `[aws]` as needed. Never put AWS secrets in this file.

## Choose the interface language

From your shell, start TermTuck with `termtuck --lang zh` (Simplified Chinese), `termtuck --lang en` (English), or `termtuck --lang ja` (Japanese).

`/lang` is a **chat command**, not a shell command: do not run `termtuck /lang`. Inside chat, `/lang` lists the current language and available choices. `/lang zh`, `/lang en`, and `/lang ja` switch the UI immediately and save the choice locally. Reopen TermTuck without `--lang` to use the saved language.

Startup order: `--lang` → last saved interactive selection → `language` under `[ui]` in config → Simplified Chinese. For example:

```toml
[ui]
language = "zh"
```

`--no-history` skips both reading and writing the last selected UI language. The CLI flag and config still work, and in-chat switches apply in memory for the current run only. UI language changes neither the prompt sent to the model nor the language of its replies.

## Chat and attach images

- Press **Enter** to send and **Esc**, then **Enter**, for a newline. Use **Ctrl+C** to interrupt streaming, `/help` for commands, and `/exit` to quit.
- In chat, use `/image "./sample shot.png"` to attach a PNG or JPEG. Use `/image list` to review queued images, `/image drop 1` to remove the first one, and `/image drop all` to clear the queue. Choose a model that accepts image input.
- On Windows, capture a nonsensitive screenshot with **Win+Shift+S**. In the WSL2 chat press **Ctrl+G** or enter `/image paste`. This explicitly reads an image using Windows PowerShell over WSL interop; ordinary terminal paste is for text. If it fails, check `command -v powershell.exe` in WSL or attach a file instead.

Each attachment appears inline as `[Image #1]`, `[Image #2]`, and so on. You can continue editing around a marker. Press **Backspace** just after it or **Delete** just before it to remove the entire attachment. Only the remaining images are sent; the markers themselves are not sent as text or saved in input history.

Attachments are sent only when you send a message. The message, attached images, and any retained context text go to Bedrock. Local chat history is not encrypted, and image bytes are not saved to history by default. Use `/history` to list saved chats and `/resume <ID>` to reopen one.

## Validation limits

Automated tests use synthetic images, mocked PowerShell and Bedrock responses, and simulated terminal key input. The optional local AWS CLI v2 test exercises profile and credential handling without calling AWS services. These checks do not validate a real Windows screenshot transfer or live AWS authentication and inference.

To check your setup, run `termtuck --check-clipboard` in WSL with a nonsensitive Windows screenshot on the clipboard. It should report image dimensions and size without contacting Bedrock. Then attach the screenshot in chat and send it with a short test message to an authorized, image-capable model. Confirm that the reply streams and that token usage is reported. To check UI persistence separately, switch with `/lang ja`, exit, and restart without `--lang`; `/lang` should show `ja`. Then start with `--no-history`, switch to `en` in chat, and exit. On the next normal launch without `--lang`, `/lang` should still show `ja`.
