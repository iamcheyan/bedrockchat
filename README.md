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
| 粘贴 Windows 截图 | Ctrl+G（需 WSL interop）；本地诊断 `bedrockchat --check-clipboard` |
| 删除指定待发图片 | 对占位符用 Backspace 或 Delete |
| 图片文件、列表或移除 | `/image <路径>`、`/image list`、`/image drop [序号或 all]` |
| 会话记忆 | `/new`、`/history [关键词]`、`/resume <ID>`、`/show`、`/rename`、`/delete`、`/export` |
| 输入历史 | 方向键、Ctrl+R |
| 用量与上下文 | `/tokens` 查看实际用量，`/clear` 只清空发送上下文 |
| 界面语言 | `/lang zh`、`/lang en`、`/lang ja`，也可用 `--lang` |

只在发送时才请求模型。默认不发送系统提示词，不重发历史图片，不调用工具、自动总结、自动重试或后台检查更新。输入预算按完整轮次裁剪，模型服务没有返回用量时显示未知。Windows 剪贴板需在有 Windows Terminal + WSL2 的机器上验收。

## 发布

推送 `go-rewrite` 会运行 Go 测试并构建 Linux `amd64` / `arm64`。要正式发版，在 GitHub Actions 运行 **Go release** 并选 `go-rewrite`，指定 `patch`（默认）、`minor` 或 `major`，或用：

```bash
gh workflow run go-release.yml --ref go-rewrite -f bump=patch
```

工作流自动分配下一个 `vX.Y.Z`，把版本写入二进制并发布两种架构及 `SHA256SUMS`。普通分支推送不会自动成为正式 Release。
