# bedrockchat · Go 版

面向 Linux / WSL2 的终端模型聊天工具。输入框只显示 `›`，没有 `You` 标签和底部提示栏。启动时显示当前模型；聊天内用 `/model` 查看，或在脚本中运行 `bedrockchat --current-model`（不会调用模型）。支持颜色的终端会用灰色显示状态、用量和其他程序提示。回复生成时显示一行灰色预览；完成后只保留渲染后的 Markdown，支持标题、列表、表格、引用和代码语法高亮。历史保留原始 Markdown，`/show` 用相同方式渲染。设置 `NO_COLOR=1` 或重定向输出时，保留原始 Markdown 的纯文本流式输出。

## 安装与升级

从 [GitHub Releases](https://github.com/iamcheyan/bedrockchat/releases/latest) 下载与你的架构匹配的 `bedrockchat-linux-amd64`（x86_64）或 `bedrockchat-linux-arm64`（aarch64），以及 `SHA256SUMS`。在下载目录中先验证文件，再安装到用户目录，例如：

```bash
asset=bedrockchat-linux-amd64 # ARM64 改成 bedrockchat-linux-arm64
awk -v f="$asset" '$2 == f { print }' SHA256SUMS | sha256sum --check - &&
  install -D -m 755 "$asset" "$HOME/.local/bin/bedrockchat"
bedrockchat --version
bedrockchat update
```

确保 `~/.local/bin` 在 `PATH` 中。`update` 仅在主动运行时联网，从本仓库的最新正式 Release 选择当前架构，校验后替换当前 Go 可执行文件。不要用它更新 Python 版或包管理器托管的文件；首次从 Python 迁移时请先安装上述 Go 二进制。

从源码构建：

```bash
git clone -b go-rewrite https://github.com/iamcheyan/bedrockchat.git
cd bedrockchat
go test ./...
go build -o ./dist/bedrockchat ./cmd/bedrockchat
```

## 使用

Bedrock 仍是默认后端。已有 AWS 配置时，例如：

```bash
aws sso login --profile work-sso
bedrockchat --profile work-sso --region us-east-1 --model amazon.nova-lite-v1:0
```

如果提示 `AWS profile not found`，可用 `aws configure list-profiles` 核对名称，并用 `--profile <名称>` 明确选择。未明确设置时，若 `AWS_DEFAULT_PROFILE` 和 `AWS_PROFILE` 同时存在，普通 profile 选择沿用 Python 版的优先级，先取 `AWS_DEFAULT_PROFILE`。显式指定的 profile 不存在时会报错，不会回退到其他 profile。

若使用本机 Ollama，在服务已启动并下载模型后运行：

```bash
bedrockchat --provider openai --model qwen2.5:1.5b
```

默认连接 `http://localhost:11434/v1/chat/completions`。其他地址可用 `--endpoint` 指定完整 URL；仅回环地址允许 HTTP。兼容端点目前不支持需要 API Key 的服务。只返回非流式 JSON 的服务可在配置中设置 `[openai]` 的 `stream = false`。

[配置示例](config.example.toml)以及既有配置、会话和偏好目录仍兼容 Python 版；默认继续读取 `~/.config/bedrock-chat/config.toml`，状态保存在 `~/.local/state/bedrock-chat` 及兼容端点的独立目录。`--no-history` 不读写历史或上次选择。原 Python 版仍在 [`main`](https://github.com/iamcheyan/bedrockchat/tree/main)。

| 操作 | 命令或按键 |
|---|---|
| 发送 / 换行 / 中断 | Enter / Esc 后按 Enter / Ctrl+C |
| 查看或切换模型 | `/model`、`/model <ID 或别名>` |
| 查看或切换模型推理 | `/think`、`/think on`、`/think off`、`/think auto` |
| 粘贴 Windows 截图 | Ctrl+G（需 WSL interop）；本地诊断 `bedrockchat --check-clipboard` |
| 删除指定待发图片或文本块 | 对占位符用 Backspace 或 Delete |
| 多行或长文本粘贴 | 终端普通粘贴；多行或超过 1000 字符会折叠 |
| 展开粘贴文本继续编辑 | 光标放在文本块前或后，按 Ctrl+X 再 Ctrl+E |
| 图片文件、列表或移除 | `/image <路径>`、`/image list`、`/image drop [序号或 all]` |
| 会话记忆 | `/new`、`/history [关键词]`、`/resume <ID>`、`/show`、`/rename`、`/delete`、`/export` |
| 输入历史 | 方向键、Ctrl+R |
| 用量与上下文 | `/tokens` 查看实际用量，`/clear` 只清空发送上下文 |
| 界面语言 | `/lang zh`、`/lang en`、`/lang ja`，也可用 `--lang` |

只在发送时才请求模型。默认不发送系统提示词，不重发历史图片，不调用工具、自动总结、自动重试或后台检查更新。输入预算按完整轮次裁剪，模型服务没有返回用量时显示未知。Windows 剪贴板需在有 Windows Terminal + WSL2 的机器上验收。

### 多行文本与代码粘贴

从网页、编辑器或日志窗口复制内容后，用终端普通粘贴（Windows Terminal 通常为 Ctrl+Shift+V）。在支持 bracketed paste 的终端/tmux 中，多行文本会在光标处折叠成 `[粘贴 #1 · 12 行]`，超过 1000 字符的单行内容也会折叠。可混合输入文字、图片和多个文本块，粘贴不会自动发送，也不会触发图片剪贴板读取。

用方向键跨过整个块；Backspace 或 Delete 删除该块，Ctrl+X 再 Ctrl+E 展开光标旁的文本块，之后像普通文字一样编辑。Enter 发送时在原位置展开剩余文本，保留换行、空白、代码缩进和 Markdown；历史与模型收到的都是实际文本，不是占位符。折叠只整理界面，不减少这些文本的 Token，仍受输入预算限制。

终端接收的是剪贴板中的纯文本，不保留网页字体、颜色或 HTML/RTF 排版，也不自动上传文件。每次粘贴上限 1 MiB，超限会提示且不插入。输入历史恢复的是展开后的文字，图片不会自动重发。

实现方式参考 [Codex 的大段粘贴占位符](https://github.com/openai/codex/blob/afe555b9d3e95b4f58b247212565070f1555bc5d/codex-rs/tui/src/bottom_pane/chat_composer.rs)和 [OpenCode 的粘贴文本块](https://github.com/anomalyco/opencode/blob/08cc1967e2bbf97e12381d25f9ae58cb5c74a51b/packages/tui/src/component/prompt/index.tsx#L1444-L1516)，没有引入 Agent 框架。

### 本地视觉模型响应超时

Qwen3-VL 等本地模型可能需要较长时间加载权重或编码图片。兼容后端默认允许等待 **5 分钟**而不是旧版的 60 秒；这是等待响应数据的空闲上限，每收到一批数据会重新计时，不是总运行时限。连接超时仍为 5 秒，Bedrock 的等待设置不变。可用 `Ctrl+C` 随时取消，不会自动重试请求。

例如，在 Ollama 已运行并下载模型后：

```bash
bedrockchat --provider openai --model qwen3-vl:4b --response-timeout 10m
```

也可在配置文件 `[openai]` 下设置 `response_timeout = "10m"`（支持 1 秒至 1 小时）；`/config` 会显示有效值。命令行参数优先。若仍超时，检查 Ollama 服务是否仍在加载或推理，以及内存、显存是否足够；先试一张较小的图片，或在服务支持时用 `/think off` 降低推理耗时。当前开发环境没有运行真实 Qwen3-VL，慢响应路径使用本地 HTTP 模拟服务验证。

### 模型推理开关

`/think on` 请求开启扩展推理，`/think off` 请求关闭，`/think auto` 恢复模型默认行为（也是初始默认，不发送额外推理参数）。切换立即影响后续请求，但不会修改配置文件。`/think` 或 `/config` 可查看当前模式。启动时用 `bedrockchat --thinking off`，或在配置的 `[chat]` 中设置 `thinking = "off"` 来固定启动模式；命令行参数优先。

- **AWS Bedrock**：支持识别出的 Claude 推理模型，包括 `jp.anthropic.claude-haiku-5-5`。旧版手动推理模型使用 1024 Token 的思考预算，开启时 `--max-output-tokens` 必须大于 1024，例如 2048。不能关闭推理的模型或未识别的模型 ID 会报提示，可用 `auto` 恢复兼容模式。
- **Ollama / OpenAI 兼容端点**：分别发送 `reasoning_effort: "low"` 或 `"none"`。端点和模型必须支持相应值；例如 Ollama 的 GPT-OSS 会把 `none` 当作低推理而不是完全关闭。端点拒绝参数时，请用 `auto`，程序不会自动重发请求。

开关控制的是模型请求，不是推理过程的显示。单独返回的推理字段不会显示、保存或加入下一轮；启用推理可能增加耗时和输出 Token。参考 [AWS Bedrock 自适应推理](https://docs.aws.amazon.com/bedrock/latest/userguide/claude-messages-adaptive-thinking.html)、[Ollama 兼容接口](https://docs.ollama.com/api/openai-compatibility)。

## 发布

推送 `go-rewrite` 会运行 Go 测试并构建 Linux `amd64` / `arm64`。要正式发版，在 GitHub Actions 运行 **Go release** 并选 `go-rewrite`，指定 `patch`（默认）、`minor` 或 `major`，或用：

```bash
gh workflow run go-release.yml --ref go-rewrite -f bump=patch
```

工作流自动分配下一个 `vX.Y.Z`，把版本写入二进制并发布两种架构及 `SHA256SUMS`。普通分支推送不会自动成为正式 Release。
