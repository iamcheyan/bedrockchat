# TermTuck

简体中文 · [English](README.en.md) · [日本語](README.ja.md)

**能发图、能接着聊，又不必把整段历史重发给模型的终端 AI 聊天工具。**

TermTuck（Terminal + Tuck）面向 Windows Terminal + WSL2，支持 AWS Bedrock 和 OpenAI 兼容服务的流式回复，支持 Markdown、代码高亮和中日英多行对话。

- **图片附件**：粘贴 Windows 截图或附加本地图片，支持图文混发；只在主动发送时传给选定的后端。
- **本地会话记忆**：会话可以搜索、恢复和导出。保存的本地历史与实际发给模型的上下文相互独立。
- **按预算裁剪上下文**：优先剔除最早的完整轮次，缩放新图片，默认不重发历史图片，不为摘要额外调用模型。支持设置输出上限，并显示实际 Token 和可选费用估算。

## 安装

在 Windows PowerShell 中运行 `wsl -l -v`，确认发行版的 `VERSION` 是 `2`。如尚未安装 WSL，可参考 [Microsoft 的 WSL 安装指南](https://learn.microsoft.com/zh-cn/windows/wsl/install)。用 **Windows Terminal 的 WSL 配置文件**进入发行版；以下命令均在 WSL 内执行。需要 Python 3.11 或更新版本、Git，以及 pipx 或 uv（任选其一）。

在本仓库页面选择 **Code → HTTPS**，用 `git clone` 克隆。公开仓库不需要 GitHub 登录。克隆后运行：

```bash
cd termtuck

pipx install .
pipx ensurepath   # 若命令未加入 PATH，运行后重新打开终端
# 或：
# uv tool install .
# uv tool update-shell

termtuck --help
```

工具没有发布到 PyPI，请从仓库或其构建的 wheel 安装。旧命令 `bedrock-chat` 继续可用，配置与历史文件无需迁移。

## 界面语言

启动时任选一种：`termtuck --lang zh`（简体中文）、`termtuck --lang en`（英语）或 `termtuck --lang ja`（日语）。

`/lang` 是**聊天内命令**，不是 `termtuck /lang` 这样的 shell 命令。进入聊天后，输入 `/lang` 查看当前语言和可选语言；输入 `/lang zh`、`/lang en` 或 `/lang ja` 立即切换并在本地保存。重新打开 TermTuck 时，若未指定 `--lang`，则使用上次保存的界面语言。

启动时的优先级：`--lang` → 上次在聊天内选定并保存的语言 → TOML 的 `[ui]` 下的 `language`（[示例](config.example.toml)为 `language = "zh"`）→ 简体中文。`--no-history` 不读取或写入上次选定的界面语言，但仍可用参数或配置指定语言，也可在聊天中切换（仅在当前进程内生效）。

此设置只影响 TermTuck 的界面，不会改变发送给模型的提示词，也不控制模型回复的语言。

## AWS CLI 认证与模型

在 **WSL 内**安装 [AWS CLI v2](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)。Windows 侧的 AWS CLI 登录状态默认不会出现在 WSL 的 `~/.aws`。组织使用 IAM Identity Center 时：

```bash
aws configure sso --profile work-sso
aws sso login --profile work-sso
aws sts get-caller-identity --profile work-sso

termtuck --profile work-sso --region us-east-1 --model amazon.nova-lite-v1:0
```

以上 Region 和模型仅是示例，实际应使用账户已获授权、支持 ConverseStream 的模型；有的模型需要推理配置文件 ID/ARN，而不是底层模型 ID。IAM 至少需要对实际调用资源授予 `bedrock:InvokeModelWithResponseStream`，跨区推理还需要符合组织策略。对已配置的普通 AWS Profile，直接传 `--profile`；省略则使用 boto3 默认凭证链（包括 `AWS_PROFILE` 和已配置的默认 Profile）。SSO 过期时，重新执行 `aws sso login --profile work-sso` 后再发送，不需要输入额外 API Key。

可将常用参数写进 TOML；CLI 参数优先。Region 按 `--region` → TOML `[aws].region` → `AWS_REGION` → `AWS_DEFAULT_REGION` → AWS Profile 中的 `region` 解析。默认配置路径是 `$XDG_CONFIG_HOME/bedrock-chat/config.toml`，未设置 XDG 时是 `~/.config/bedrock-chat/config.toml`。也可用 `--config` 指定文件。程序不在当前工作目录寻找配置。

```bash
config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/bedrock-chat"
install -d -m 700 "$config_dir"
# 首次配置时执行，勿覆盖已有文件：
install -m 600 config.example.toml "$config_dir/config.toml"
```

编辑文件中的 `[aws]`（`profile`、`region`、`model_id`），然后运行 `termtuck`。`[models.<别名>]` 可配置 `id`、`vision`，以及每百万 Token 的 USD 输入、输出价格。`vision = false` 会在本地明确阻止图片；未标注则以 Bedrock 的实际响应为准，不额外请求模型能力信息。运行 `/model` 查看列表，用 `/model <别名或 ID>` 切换。新启动时的模型优先级是 `--model` → 上次选用的模型 → `[aws].model_id`。通过 `/model` 切换后立即记住，成功完成对话也会更新记录；记录只包含模型 ID，保存在历史目录的 `last-model` 文件中。`/resume` 仍使用会话自己的模型。关闭历史时不读取或保存上次模型。

## 使用本机 Ollama 或其他兼容端点

默认仍使用 Bedrock。Ollama 已在本机运行、且模型已下载时，不需要 AWS 凭证或 API Key：

```bash
termtuck --provider openai --model qwen2.5:1.5b
```

默认请求 `http://localhost:11434/v1/chat/completions`。如果地址不同，用 `--endpoint` 指定完整的 `/chat/completions` URL，或在 `config.toml` 中设置：

```toml
[chat]
provider = "openai"

[openai]
endpoint = "http://localhost:11434/v1/chat/completions"
model_id = "qwen2.5:1.5b"
stream = true
```

只支持非流式 JSON 的服务可设置 `stream = false`。`qwen2.5:1.5b` 是文本模型；发送图片时请换用服务支持的视觉模型。兼容后端目前适用于不要求 API Key 的服务；仅回环地址可用 HTTP，其他地址须用 HTTPS。请求不会自动重试、跟随重定向或切换后端。聊天记录和上次使用的模型按后端及兼容端点隔离，避免历史被误发到新地址。用 `--provider bedrock` 可切回 AWS。

参考：[Ollama 的 OpenAI 兼容接口](https://docs.ollama.com/api/openai-compatibility)。

## 输入与截图

| 操作 | 快捷键 |
|---|---|
| 发送 | **Enter** |
| 输入换行 | 先按 **Esc**，再按 **Enter**（也可用传同一序列的 Alt+Enter，取决于终端） |
| 粘贴 Windows 截图 | **Ctrl+G** |
| 移除待发送图片 | 在图片占位符后按 **Backspace**，或在其前按 **Delete**；也可按 **Ctrl+X** 后 **Ctrl+D** 移除最后一张 |
| 输入历史 | 方向键、**Ctrl+R**；只在本次程序内保存 |
| 中断流式回复 | **Ctrl+C**；可能仍会产生 AWS 用量 |

1. Windows 中按 **Win+Shift+S** 选取截图，确保截图已进入剪贴板。
2. 回到 WSL 聊天输入框按 **Ctrl+G**，光标处会出现 `[图片 #1]`。可以继续编辑占位符前后的文字，或在其他位置再粘贴一张。底栏仍显示图片尺寸和大小。
3. 用左右方向键跨过占位符，按 **Backspace** 或 **Delete** 可以整块删掉对应图片，不影响其他文字或图片。也可用 `/image drop 1` 或 `/image drop all`。
4. 按 **Enter** 发送剩余的图片和文字；占位符不会作为文字发给模型，也不会保存在输入历史中。可以只发送图片。

Windows Terminal 的普通粘贴（例如 **Ctrl+Shift+V**）继续用于文字；它通常不会把图片像素传给 WSL。**Ctrl+G** 专门触发图片读取；若被自定义 Windows Terminal 快捷键拦截，可免联网运行 `termtuck --check-clipboard` 排查，也可在聊天内输入 `/image paste`。程序通过 WSL interop 启动 Windows PowerShell 读取图片格式，不轮询剪贴板、不读剪贴板文本、不写临时截图文件。

手动附加：`/image "./sample shot.png"`。默认缩放图片长边至不超过 1568 px，优先用无损 PNG 保护细字；可在 `[images]` 调整尺寸和字节上限。特别小的文字建议先裁剪图中相关区域，避免整屏缩放。每条消息最多 20 张，合计图片字节最多 15 MB，个别模型可能有更严格的限制。

如提示找不到 `powershell.exe`，检查 `command -v powershell.exe`、Windows PowerShell 5.1、WSL interop 及 Windows PATH。`/etc/wsl.conf` 的 `[interop]` 中，`enabled = true` 允许运行 Windows 程序，通常也需要 `appendWindowsPath = true`；修改后从 Windows 运行 `wsl --shutdown` 再重新打开（会停止运行中的 WSL 实例）。共享服务器或仅有远端 Linux shell 时没有本地 Windows 剪贴板，改用文件路径。

## 命令与历史

| 命令 | 用途 |
|---|---|
| `/new [名称]` | 创建新会话 |
| `/history [关键词]` 或 `/search [关键词]` | 本地列表；按名称、消息内容搜索 |
| `/resume <ID>` 或 `/switch <ID>` | 恢复、切换会话（可用唯一前缀） |
| `/show [轮数]` | 查看当前会话最近的历史（默认 5 轮） |
| `/rename <ID> <名称>` / `/delete <ID>` | 本地重命名 / 永久删除该会话和已保存的图片 |
| `/model [别名或 ID]` | 查询或切换模型 |
| `/lang [zh\|en\|ja]` | 查看当前界面语言和选项，或切换界面语言 |
| `/clear` | 只清空后续请求上下文，保留本地历史和累计用量 |
| `/tokens` | 查看已知的累计用量及配置价格后的费用估算 |
| `/image <路径>` / `/image list` / `/image drop [序号\|all]` | 附加、查看和移除待发送附件 |
| `/image paste` / `/image reuse <轮次> <序号>` | 显式读取剪贴板 / 重新附加已保存的图片（用 `/show` 查编号） |
| `/export [路径.md]` | 导出 Markdown，默认写当前目录并且不覆盖文件 |
| `/config` / `/help` / `/exit` | 查看配置、帮助或退出 |

输入 `//` 可发送以 `/` 开头的普通文字。切换会话会丢弃待发送图片，避免附件误发到另一个会话。每轮保存在 `$XDG_STATE_HOME/bedrock-chat`（默认 `~/.local/state/bedrock-chat`）下，建议使用 WSL Linux 文件系统而不是 `/mnt/c`；目录权限 `0700`，文件 `0600`，不加密。记录包含 UTC 时间、模型、消息、已知实际用量、完成状态和附件元数据。**默认不保存图片内容**，开启 `[history] save_images = true` 才可从历史重新附加。`--no-save-images` 禁止新保存图片；`--no-history` 或 `[history] enabled = false` 禁止读写持久历史，仍可维持当前进程的聊天；显式 `/export` 例外。上述关闭选项不会删除既有文件。

## Token 和数据边界

- 默认没有系统提示词，模型请求不带工具、Function Calling、扩展思考参数或自动收集的上下文；不支持可能预置额外指令或工具的 Prompt Management ARN。不会执行生成的命令，也不会读取仓库或扫描目录作为模型上下文。
- `max_input_tokens`（默认 6000）是跨模型的**本地估算**预算，不是模型分词器测得的硬上限；请求超预算时先剔除最早的完整对话轮次，当前消息本身超预算会在联网前提示。实际模型窗口请按所选模型留出余量。`max_output_tokens`（默认 1024）是发送给所选后端的输出上限。中断、失败或只有图片而没有文本的历史轮次不会作为后续上下文重发；历史图片字节从不自动重发。只有显式发送消息才会发起模型请求，不自动重试、总计 Token 或总结历史。
- 回复结束显示后端返回的 Token 输入、输出、总量和当前会话累计。断流时如果没拿到用量，会明确显示未知，不按零费用算。费用按已知用量及配置价格估算，不代表发票、模型缓存价格或其它费用。
- 不上传历史文件、不自动转发图片或企业资料、不收集遥测、不保存 AWS 凭证。**按 Enter 主动发送的消息、已附加图片及预算内的历史文本会发送到选定的后端**：AWS Bedrock，或明确启用的兼容端点。请仅向受信任的服务发送获准处理的数据。AWS SDK 在实际请求时可以按选定凭证机制访问 SSO、STS 等 AWS 认证服务；组织应单独审查 IAM、跨区推理、代理及 Bedrock 推理日志设置。

## 开发与验收

```bash
uv sync --locked
uv run pytest -q
uv run ruff check .
uv run ruff format --check .
uv build
```

单元及功能测试包含 Bedrock 事件流、Mock PowerShell 进程、合成图片和 prompt_toolkit 按键测试。

### 本地 AWS CLI 认证模拟

可选用系统已有的 AWS CLI v2；它不是运行时新增依赖。以下命令让真正的 `aws configure set` 在临时 HOME 下写入合成 Profile，再由 TermTuck 读取。未指定 `BEDROCK_TEST_AWS_CLI` 时，仅跳过这项需要真实 AWS CLI 的测试。

```bash
BEDROCK_TEST_AWS_CLI="$(command -v aws)" uv run --locked pytest -q tests/test_aws_cli_lab.py
```

测试验证显式 Profile 和 `AWS_PROFILE` 读取共享凭证、随请求发送会话凭证的 SigV4 签名路径，以及 IAM Identity Center 本地缓存有效、过期和缺失时的行为。boto3/botocore 的凭证解析、签名和二进制 EventStream 解码实际执行；SSO 角色凭证和 Bedrock 响应仅在测试内模拟。不会访问真实 AWS API、修改现有 `~/.aws`，或消耗模型 Token。参考 AWS 官方文档：[配置与凭证文件](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html)、[IAM Identity Center](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-sso.html)、[Region 环境变量](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-envvars.html#envvars-list-AWS_REGION)及 [boto3 凭证链](https://docs.aws.amazon.com/boto3/latest/guide/credentials.html)。

### 真机验收

未在开发机上向真实 AWS 登录或调用 Bedrock，也未在真实 Windows Terminal + WSL2 上跑截图链路（开发机是 Debian，无 Windows 会话和 AWS 登录）。真机验收步骤：

1. 截取一张**不含敏感数据**的窗口图片，在 WSL 运行 `termtuck --check-clipboard`，预期显示正确尺寸和非零大小，不产生模型调用。
2. 运行 `termtuck`；输入半句文字，按 `Ctrl+G` 并确认草稿还在。再粘贴一张、移除其中一张，并确认底部数字随操作变化；另外检查普通文字粘贴和 `Esc` 后 `Enter` 换行。
3. 用允许视觉输入的已授权模型发送图片和测试文字，确认收到流式回复和实际 Token；再追问一句验证多轮。发送一条较长的测试请求并按 `Ctrl+C`，确认能继续输入，且未知用量不显示为零。
4. 切换到配置为 `vision = false` 的模型，确认附加图片被拒绝；待测试 Profile 的 SSO 会话自然过期后，确认提示重新登录。
