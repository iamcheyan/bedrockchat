package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/iamcheyan/bedrockchat/internal/chat"
	"github.com/iamcheyan/bedrockchat/internal/images"
	"github.com/iamcheyan/bedrockchat/internal/store"
	"github.com/iamcheyan/bedrockchat/internal/terminal"
	"github.com/iamcheyan/bedrockchat/internal/update"
	"golang.org/x/term"
)

var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, in *os.File, out, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "update" {
		if len(args) != 1 {
			fmt.Fprintln(stderr, "Usage: bedrockchat update")
			return 2
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := update.Run(ctx, version, out); err != nil {
			fmt.Fprintln(stderr, clean(err.Error()))
			return 1
		}
		return 0
	}
	var opt store.Options
	var showVersion, clipboard, current bool
	flags := flag.NewFlagSet("bedrockchat", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opt.ConfigPath, "config", "", "TOML config file")
	flags.StringVar(&opt.Provider, "provider", "", "bedrock or openai")
	flags.StringVar(&opt.Endpoint, "endpoint", "", "full Chat Completions URL")
	flags.StringVar(&opt.Region, "region", "", "AWS Region")
	flags.StringVar(&opt.Profile, "profile", "", "AWS Profile")
	flags.StringVar(&opt.Model, "model", "", "model ID or alias")
	flags.StringVar(&opt.Language, "lang", "", "zh, en or ja")
	flags.IntVar(&opt.MaxInput, "max-input-tokens", 0, "estimated input token budget")
	flags.IntVar(&opt.MaxOutput, "max-output-tokens", 0, "maximum output tokens")
	flags.BoolVar(&opt.NoHistory, "no-history", false, "do not read or save chat history or preferences")
	flags.BoolVar(&opt.NoSaveImages, "no-save-images", false, "do not save new image bytes")
	flags.BoolVar(&clipboard, "check-clipboard", false, "read Windows clipboard image locally and report its size")
	flags.BoolVar(&current, "current-model", false, "print the effective model and exit")
	flags.BoolVar(&showVersion, "version", false, "print version and exit")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: bedrockchat [options] | bedrockchat update")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	if showVersion {
		fmt.Fprintln(out, "bedrockchat", version)
		return 0
	}
	cfg, err := store.Load(opt)
	if err != nil {
		fmt.Fprintln(stderr, clean(err.Error()))
		return 1
	}
	if clipboard {
		img, e := images.Paste(context.Background(), cfg.MaxEdge, cfg.MaxBytes)
		if e != nil {
			fmt.Fprintln(stderr, clean(e.Error()))
			return 1
		}
		fmt.Fprintf(out, "%d×%d · %.1f KiB · %s\n", img.Width, img.Height, float64(len(img.Data))/1024, img.Format)
		return 0
	}
	h := store.Store{Root: cfg.ConversationDir, Enabled: cfg.History, SaveImages: cfg.SaveImages}
	p := store.Store{Root: cfg.StateDir, Enabled: cfg.History}
	model := cfg.Model
	if opt.Model == "" {
		if last := h.LastModel(); last != "" {
			model = last
		}
	}
	if current {
		if model != "" {
			fmt.Fprintln(out, clean(model))
		}
		return 0
	}
	if !term.IsTerminal(int(in.Fd())) {
		fmt.Fprintln(stderr, "bedrockchat requires an interactive terminal")
		return 1
	}
	lang := cfg.Language
	if opt.Language == "" {
		if saved := p.Language(); saved != "" {
			lang = saved
		}
	}
	var backend chat.Backend
	if cfg.Provider == "openai" {
		backend = chat.NewOpenAI(cfg.Endpoint, cfg.Stream)
	} else {
		backend = chat.NewBedrock(cfg.Region, cfg.Profile)
	}
	a := &app{cfg: cfg, history: h, prefs: p, lang: lang, backend: backend, out: out, editor: terminal.New(in, out)}
	a.session = h.New(a.tr("未命名", "Untitled", "無題"), model)
	if f, ok := out.(*os.File); ok && term.IsTerminal(int(f.Fd())) && os.Getenv("TERM") != "dumb" && os.Getenv("NO_COLOR") == "" {
		width, _, _ := term.GetSize(int(f.Fd()))
		if width < 30 {
			width = 80
		}
		if width > 100 {
			width = 100
		}
		a.markdown, _ = glamour.NewTermRenderer(glamour.WithStylePath("dark"), glamour.WithWordWrap(width))
	}
	a.editor.ImageLabel = func(_ chat.Image) string { return a.tr("[图片]", "[Image]", "[画像]") }
	a.line("bedrockchat · /help")
	a.showModel()
	for {
		parts, e := a.editor.Read(a.draft, a.paste, func(e error) { a.problem(e) })
		if errors.Is(e, io.EOF) {
			return 0
		}
		if errors.Is(e, terminal.ErrInterrupted) {
			a.draft = nil
			continue
		}
		if e != nil {
			fmt.Fprintln(stderr, clean(e.Error()))
			return 1
		}
		if !a.dispatch(parts) {
			return 0
		}
	}
}

type app struct {
	cfg            store.Settings
	history, prefs store.Store
	session        store.Session
	lang           string
	backend        chat.Backend
	out            io.Writer
	editor         *terminal.Editor
	draft          []terminal.Part
	markdown       *glamour.TermRenderer
}

func (a *app) tr(zh, en, ja string) string {
	switch a.lang {
	case "en":
		return en
	case "ja":
		return ja
	}
	return zh
}
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if (r < 32 && r != '\n' && r != '\t') || (r >= 127 && r <= 159) {
			return -1
		}
		return r
	}, strings.ReplaceAll(s, "\r\n", "\n"))
}
func (a *app) line(s string) { fmt.Fprintln(a.out, clean(s)) }
func (a *app) problem(e error) {
	if e != nil {
		a.line(a.tr("错误：", "Error: ", "エラー：") + e.Error())
	}
}
func (a *app) showModel() {
	model := a.session.Model
	if model == "" {
		model = a.tr("未设置", "not set", "未設定")
	}
	a.line(a.tr("当前模型：", "Current model: ", "現在のモデル：") + model + " · " + a.cfg.Provider)
}
func (a *app) plainReply(s string) { a.line(s) }
func (a *app) renderReply(s string) {
	if a.markdown != nil {
		if rendered, err := a.markdown.Render(clean(s)); err == nil {
			fmt.Fprint(a.out, rendered)
			return
		}
	}
	a.plainReply(s)
}
func (a *app) textOnly() bool { v := a.cfg.ModelInfo(a.session.Model).Vision; return v != nil && !*v }
func (a *app) imageBlocked() error {
	return errors.New(a.tr("当前模型不支持图片。", "The current model does not support images.", "現在のモデルは画像に対応していません。"))
}
func (a *app) paste() (chat.Image, error) {
	if a.textOnly() {
		return chat.Image{}, a.imageBlocked()
	}
	return images.Paste(context.Background(), a.cfg.MaxEdge, a.cfg.MaxBytes)
}

func (a *app) dispatch(parts []terminal.Part) bool {
	var text strings.Builder
	var imgs []chat.Image
	var pending []terminal.Part
	for _, p := range parts {
		if p.Image != nil {
			imgs = append(imgs, *p.Image)
			pending = append(pending, p)
		} else {
			text.WriteString(p.Text)
		}
	}
	msg := text.String()
	if strings.HasPrefix(msg, "/") && !strings.HasPrefix(msg, "//") && !strings.Contains(msg, "\n") {
		a.draft = pending
		if a.editor != nil {
			a.editor.Remember(msg)
		}
		return a.command(msg)
	}
	if strings.TrimSpace(msg) == "" && len(imgs) == 0 {
		a.draft = nil
		return true
	}
	a.draft = parts
	if strings.HasPrefix(msg, "//") {
		msg = msg[1:]
	}
	if a.session.Model == "" {
		a.problem(errors.New(a.tr("请先用 /model <ID> 设置模型。", "Choose a model with /model <ID>.", "/model <ID> でモデルを選んでください。")))
		return true
	}
	if len(imgs) > 0 && a.textOnly() {
		a.problem(a.imageBlocked())
		return true
	}
	total := 0
	for _, i := range imgs {
		total += len(i.Data)
	}
	if len(imgs) > 20 || total > 15_000_000 {
		a.problem(errors.New(a.tr("最多 20 张图片，合计不超过 15 MB。", "Attach at most 20 images and 15 MB total.", "画像は最大 20 枚、合計 15 MB までです。")))
		return true
	}
	var history []chat.Pair
	for _, t := range a.session.Turns[min(max(a.session.ContextStart, 0), len(a.session.Turns)):] {
		if t.Status == "complete" && strings.TrimSpace(t.User) != "" && strings.TrimSpace(t.Assistant) != "" {
			history = append(history, chat.Pair{User: t.User, Assistant: t.Assistant})
		}
	}
	req, estimated, dropped, err := chat.Trim(chat.Request{Model: a.session.Model, Text: msg, System: a.cfg.System, History: history, Images: imgs, MaxOutput: a.cfg.MaxOutput}, a.cfg.MaxInput)
	if err != nil {
		a.problem(err)
		return true
	}
	if a.editor != nil && msg != "" {
		a.editor.Remember(msg)
	}
	a.line(fmt.Sprintf(a.tr("预计输入 %d Token · 略去 %d 轮 · Ctrl+C 中断", "Estimated input: %d tokens · %d turns omitted · Ctrl+C to cancel", "入力推定 %d トークン · %d ターン省略 · Ctrl+C で中断"), estimated, dropped))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	var streamed bool
	result := a.backend.Send(ctx, req, func(s string) { streamed = true; fmt.Fprint(a.out, clean(s)) })
	canceled := ctx.Err() != nil
	cancel()
	if streamed {
		fmt.Fprintln(a.out)
	}
	if result.Text != "" {
		if !streamed {
			a.renderReply(result.Text)
		} else if a.markdown != nil && (strings.Contains(result.Text, "```") || strings.Contains(result.Text, "**") || strings.HasPrefix(result.Text, "#")) {
			a.renderReply(result.Text)
		}
	}
	status := "complete"
	if canceled || result.StopReason == "interrupted" {
		status = "interrupted"
	} else if result.Err != nil {
		status = "error"
	}
	a.problem(result.Err)
	if result.Err == nil && result.StopReason == "max_tokens" {
		a.line(a.tr("已达到输出上限。", "Output token limit reached.", "出力トークン上限に達しました。"))
	}
	t := store.Turn{Model: a.session.Model, User: msg, Assistant: result.Text, Usage: result.Usage, Status: status, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}
	for _, img := range imgs {
		t.Attachments = append(t.Attachments, store.Attachment{Name: img.Name, Format: img.Format, Width: img.Width, Height: img.Height, Size: len(img.Data)})
	}
	before := len(a.session.Turns)
	if e := a.history.SaveTurn(&a.session, t, imgs); e != nil {
		a.problem(e)
		if len(a.session.Turns) == before {
			a.session.Turns = append(a.session.Turns, t)
		}
	}
	if status == "complete" {
		a.draft = nil
		a.problem(a.history.SaveModel(a.session.Model))
	} else {
		a.draft = pending
	}
	a.tokens(t.Usage, t.Model)
	return true
}

func (a *app) tokens(usage *chat.Usage, model string) {
	if model != "" {
		if usage == nil {
			a.line(a.tr("本次 Token：未知", "Request tokens: unknown", "今回のトークン：不明"))
		} else {
			a.line(fmt.Sprintf(a.tr("本次 Token：输入 %d / 输出 %d / 总计 %d", "Request tokens: input %d / output %d / total %d", "今回のトークン：入力 %d / 出力 %d / 合計 %d"), usage.InputTokens, usage.OutputTokens, usage.TotalTokens))
			if v, ok := a.cost(model, usage); ok {
				a.line(fmt.Sprintf(a.tr("预估 US$%.8f", "Estimated US$%.8f", "推定 US$%.8f"), v))
			}
		}
	}
	var tally chat.Usage
	unknown, priced := 0, 0
	total := 0.0
	for _, t := range a.session.Turns {
		if t.Usage == nil {
			unknown++
			continue
		}
		tally.InputTokens += t.Usage.InputTokens
		tally.OutputTokens += t.Usage.OutputTokens
		tally.TotalTokens += t.Usage.TotalTokens
		if c, ok := a.cost(t.Model, t.Usage); ok {
			total += c
			priced++
		}
	}
	a.line(fmt.Sprintf(a.tr("会话累计：输入 %d / 输出 %d / 总计 %d（未知 %d 次）", "Session tokens: input %d / output %d / total %d (%d unknown)", "累計トークン：入力 %d / 出力 %d / 合計 %d（不明 %d 回）"), tally.InputTokens, tally.OutputTokens, tally.TotalTokens, unknown))
	if priced > 0 {
		a.line(fmt.Sprintf(a.tr("已配置价格部分：US$%.8f", "Estimated cost for priced usage: US$%.8f", "価格設定分の推定費用：US$%.8f"), total))
	}
}
func (a *app) cost(model string, u *chat.Usage) (float64, bool) {
	m := a.cfg.ModelInfo(model)
	if m.InputPerMillion == nil || m.OutputPerMillion == nil {
		return 0, false
	}
	return (float64(u.InputTokens)**m.InputPerMillion + float64(u.OutputTokens)**m.OutputPerMillion) / 1e6, true
}

func splitWords(s string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	escaped, started := false, false
	for _, r := range s {
		switch {
		case escaped:
			word.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
			started = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			started = true
		case r == ' ' || r == '\t':
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if escaped || quote != 0 {
		return nil, errors.New("unfinished quote or escape")
	}
	if started {
		args = append(args, word.String())
	}
	return args, nil
}
func count(s string) (int, error) {
	n, e := strconv.Atoi(s)
	if e != nil || n < 1 {
		return 0, errors.New("expected a positive number")
	}
	return n, nil
}
func (a *app) command(msg string) bool {
	words, err := splitWords(msg)
	if err != nil {
		a.problem(err)
		return true
	}
	if len(words) == 0 {
		return true
	}
	args := words[1:]
	bad := func() {
		a.line(a.tr("命令或参数不正确，请查看 /help。", "Unknown command or invalid arguments. See /help.", "コマンドまたは引数が不正です。/help を確認してください。"))
	}
	switch strings.ToLower(words[0]) {
	case "/exit":
		if len(args) == 0 {
			return false
		}
		bad()
	case "/model":
		if len(args) == 0 {
			a.showModel()
			keys := make([]string, 0, len(a.cfg.Models))
			for k := range a.cfg.Models {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				a.line("  " + k + " · " + a.cfg.Models[k].ID)
			}
		} else if len(args) == 1 {
			model, e := a.cfg.ResolveModel(args[0])
			if e != nil {
				a.problem(e)
				break
			}
			next := a.session
			next.Model = model
			if e = a.history.State(&next); e != nil {
				a.problem(e)
				break
			}
			a.session = next
			a.problem(a.history.SaveModel(model))
			a.showModel()
		} else {
			bad()
		}
	case "/new":
		name := strings.Join(args, " ")
		if name == "" {
			name = a.tr("未命名", "Untitled", "無題")
		}
		s := a.history.New(name, a.session.Model)
		if e := a.history.State(&s); e != nil {
			a.problem(e)
			break
		}
		a.session = s
		a.draft = nil
		a.line(s.ID + " · " + s.Name)
	case "/resume", "/switch":
		if len(args) != 1 {
			bad()
			break
		}
		s, e := a.history.Load(args[0])
		if e != nil {
			a.problem(e)
			break
		}
		a.session = s
		a.draft = nil
		a.line(s.ID + " · " + s.Name)
		a.showModel()
	case "/history", "/search":
		sessions, e := a.history.List(strings.Join(args, " "))
		if e != nil {
			a.problem(e)
			break
		}
		if len(sessions) == 0 {
			a.line(a.tr("没有匹配会话。", "No matching sessions.", "該当する会話はありません。"))
		}
		for _, s := range sessions {
			a.line(s.ID[:min(len(s.ID), 12)] + " · " + s.Name)
		}
	case "/rename":
		if len(args) < 2 {
			bad()
			break
		}
		s, e := a.history.Load(args[0])
		if e != nil {
			a.problem(e)
			break
		}
		s.Name = strings.Join(args[1:], " ")
		if e = a.history.State(&s); e != nil {
			a.problem(e)
			break
		}
		if a.session.ID == s.ID {
			a.session.Name = s.Name
		}
		a.line(s.Name)
	case "/delete":
		if len(args) != 1 {
			bad()
			break
		}
		s, e := a.history.Load(args[0])
		if e != nil {
			a.problem(e)
			break
		}
		if e = a.history.Delete(s.ID); e != nil {
			a.problem(e)
			break
		}
		if a.session.ID == s.ID {
			a.session = a.history.New(a.tr("未命名", "Untitled", "無題"), a.session.Model)
			a.draft = nil
		}
		a.line(a.tr("已删除", "Deleted", "削除しました"))
	case "/clear":
		if len(args) != 0 {
			bad()
			break
		}
		next := a.session
		next.ContextStart = len(next.Turns)
		if e := a.history.State(&next); e != nil {
			a.problem(e)
			break
		}
		a.session = next
		a.line(a.tr("已清空模型上下文。", "Model context cleared.", "モデルに送る文脈を消去しました。"))
	case "/show":
		n := 5
		if len(args) > 1 {
			bad()
			break
		}
		if len(args) == 1 {
			if n, err = count(args[0]); err != nil {
				a.problem(err)
				break
			}
		}
		start := max(len(a.session.Turns)-n, 0)
		for i, t := range a.session.Turns[start:] {
			a.line(fmt.Sprintf("#%d · %s · %s · %s", start+i+1, t.Timestamp, t.Model, t.Status))
			a.line(t.User)
			for j, img := range t.Attachments {
				a.line(fmt.Sprintf("  #%d %s · %d×%d · %d B", j+1, img.Name, img.Width, img.Height, img.Size))
			}
			a.renderReply(t.Assistant)
		}
	case "/tokens":
		if len(args) != 0 {
			bad()
			break
		}
		a.tokens(nil, "")
	case "/export":
		if len(args) > 1 {
			bad()
			break
		}
		p := "bedrock-chat-" + a.session.ID[:min(len(a.session.ID), 12)] + ".md"
		if len(args) == 1 {
			p = expandPath(args[0])
		}
		if e := a.history.Export(a.session, p); e != nil {
			a.problem(e)
		} else {
			a.line(p)
		}
	case "/image":
		a.imageCommand(args)
	case "/lang":
		if len(args) == 0 {
			a.line(a.lang + " · zh 简体中文 / en English / ja 日本語")
		} else if len(args) == 1 && (args[0] == "zh" || args[0] == "en" || args[0] == "ja") {
			a.lang = args[0]
			a.problem(a.prefs.SaveLanguage(a.lang))
			a.line(a.tr("已切换为简体中文。", "Switched to English.", "日本語に切り替えました。"))
		} else {
			bad()
		}
	case "/config":
		if len(args) != 0 {
			bad()
			break
		}
		a.showModel()
		if a.cfg.Provider == "openai" {
			a.line(a.cfg.Endpoint)
		} else {
			a.line("AWS Region: " + a.cfg.Region + " · Profile: " + a.cfg.Profile)
		}
		a.line(fmt.Sprintf("max_input_tokens=%d · max_output_tokens=%d · history=%t · save_images=%t · lang=%s", a.cfg.MaxInput, a.cfg.MaxOutput, a.cfg.History, a.cfg.SaveImages, a.lang))
	case "/help":
		a.line("/model [ID] · /new [name] · /history [query] · /resume <ID>\n/show [n] · /rename <ID> <name> · /delete <ID> · /clear · /tokens\n/image <path|paste|list|drop|reuse> · /export [path] · /config · /lang [zh|en|ja] · /exit")
		a.line(a.tr("Enter 发送 · Esc+Enter 换行 · Ctrl+G 粘贴图片 · Backspace/Delete 删除图片 · Ctrl+R 查输入历史", "Enter send · Esc+Enter newline · Ctrl+G paste image · Backspace/Delete remove image · Ctrl+R search input history", "Enter 送信 · Esc+Enter 改行 · Ctrl+G 画像を貼り付け · Backspace/Delete 画像削除 · Ctrl+R 入力履歴検索"))
	default:
		bad()
	}
	return true
}
func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, e := os.UserHomeDir(); e == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
func (a *app) imageCommand(args []string) {
	if len(args) == 0 || len(args) == 1 && args[0] == "list" {
		for i, p := range a.draft {
			if p.Image != nil {
				a.line(fmt.Sprintf("#%d %s · %d×%d · %.1f KiB", i+1, p.Image.Name, p.Image.Width, p.Image.Height, float64(len(p.Image.Data))/1024))
			}
		}
		if len(a.draft) == 0 {
			a.line(a.tr("没有待发送图片。", "No pending images.", "添付画像はありません。"))
		}
		return
	}
	if args[0] == "drop" && len(args) <= 2 {
		if len(args) == 2 && args[1] == "all" {
			a.draft = nil
			return
		}
		index := len(a.draft)
		var e error
		if len(args) == 2 {
			index, e = count(args[1])
		}
		if e != nil || index < 1 || index > len(a.draft) {
			a.problem(errors.New("invalid image number"))
			return
		}
		a.draft = append(a.draft[:index-1], a.draft[index:]...)
		return
	}
	if a.textOnly() {
		a.problem(a.imageBlocked())
		return
	}
	var img chat.Image
	var e error
	switch {
	case len(args) == 1 && args[0] == "paste":
		img, e = a.paste()
	case len(args) == 3 && args[0] == "reuse":
		var t, i int
		if t, e = count(args[1]); e == nil {
			i, e = count(args[2])
		}
		var path string
		if e == nil {
			path, e = a.history.Attachment(a.session, t, i)
		}
		if e == nil {
			img, e = images.Read(path, a.cfg.MaxEdge, a.cfg.MaxBytes)
		}
	case len(args) == 1:
		img, e = images.Read(expandPath(args[0]), a.cfg.MaxEdge, a.cfg.MaxBytes)
	default:
		e = errors.New("usage: /image <path> | paste | list | drop [number|all] | reuse <turn> <number>")
	}
	if e != nil {
		a.problem(e)
		return
	}
	size := len(img.Data)
	for _, p := range a.draft {
		if p.Image != nil {
			size += len(p.Image.Data)
		}
	}
	if len(a.draft) >= 20 || size > 15_000_000 {
		a.problem(errors.New("image attachment limit exceeded"))
		return
	}
	a.draft = append(a.draft, terminal.Part{Image: &img})
}
