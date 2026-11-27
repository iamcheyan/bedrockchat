// Package store manages local settings and conversations in the legacy bedrock-chat layout.
package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/iamcheyan/bedrockchat/internal/chat"
	"golang.org/x/sys/unix"
)

// Options holds command-line overrides. Empty strings and zero limits are unset.
type Options struct {
	ConfigPath, Provider, Endpoint, Region, Profile, Model, Language string
	MaxInput, MaxOutput                                              int
	NoHistory, NoSaveImages                                          bool
	Thinking, ResponseTimeout                                        string
}

type Settings struct {
	Provider, Endpoint, Region, Profile, Model, Language, System, StateDir, ConversationDir string
	MaxInput, MaxOutput, MaxEdge, MaxBytes                                                  int
	Stream, History, SaveImages                                                             bool
	Models                                                                                  map[string]ModelInfo
	Thinking                                                                                chat.Thinking
	ResponseTimeout                                                                         time.Duration
}

type ModelInfo struct {
	ID                                string
	Vision                            *bool
	InputPerMillion, OutputPerMillion *float64
}

var (
	modelRE     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.:_/-]*$`)
	aliasRE     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
	regionRE    = regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+)+-[0-9]+$`)
	authorityRE = regexp.MustCompile(`^(\[[0-9A-Fa-f:]+\]|[A-Za-z0-9.-]+)(:[0-9]+)?$`)
)

func validModel(id string) bool {
	return len(id) <= 2048 && modelRE.MatchString(id) && !strings.Contains(id, ":prompt/") && !strings.Contains(id, ":prompt-version/")
}

func validLanguage(code string) bool { return code == "zh" || code == "en" || code == "ja" }

func (s Settings) ResolveModel(choice string) (string, error) {
	if m, ok := s.Models[choice]; ok {
		return m.ID, nil
	}
	if !validModel(choice) {
		return "", errors.New("invalid model ID or ARN (Prompt Management ARNs are not supported)")
	}
	return choice, nil
}

func (s Settings) ModelInfo(id string) ModelInfo {
	for _, m := range s.Models {
		if m.ID == id {
			return m
		}
	}
	return ModelInfo{ID: id}
}

func canonicalEndpoint(value string) (string, error) {
	bad := errors.New("endpoint must be a full /chat/completions URL without credentials, query, or fragment; HTTP is allowed only on loopback")
	for _, c := range value {
		if c < '!' || c > '~' || strings.ContainsRune(`\?#`, c) {
			return "", bad
		}
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || !authorityRE.MatchString(u.Host) {
		return "", bad
	}
	// Preserve the raw path (including escape spelling), as Python does when hashing the namespace.
	_, rest, ok := strings.Cut(value, "://")
	i := strings.IndexByte(rest, '/')
	if !ok || i < 0 || !strings.HasSuffix(rest[i:], "/chat/completions") {
		return "", bad
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")) {
		return "", bad
	}
	authority := host
	if strings.HasPrefix(u.Host, "[") {
		// URL parsing alone accepts invalid bracketed addresses.
		addr, err := netip.ParseAddr(host)
		if err != nil || !addr.Is6() {
			return "", bad
		}
		authority = "[" + host + "]"
	}
	if p := u.Port(); p != "" {
		port, err := strconv.ParseUint(strings.TrimLeft(p, "0"), 10, 16)
		if err != nil || port == 0 {
			return "", bad
		}
		if !(u.Scheme == "https" && port == 443 || u.Scheme == "http" && port == 80) {
			authority += ":" + strconv.FormatUint(port, 10)
		}
	}
	return u.Scheme + "://" + authority + rest[i:], nil
}

func xdgDir(key, fallback string) (string, error) {
	base := os.Getenv(key)
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil || !filepath.IsAbs(home) {
			return "", errors.New("cannot determine home directory")
		}
		base = filepath.Join(home, fallback)
	}
	return filepath.Join(base, "bedrock-chat"), nil
}

func readConfig(path string) (map[string]any, error) {
	// Config paths are trusted user inputs and may be dotfile symlinks. Refuse to
	// read pipes or devices (and never echo TOML parser errors, which include values).
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("configuration is not a regular file")
	}
	var raw map[string]any
	_, err = toml.NewDecoder(f).Decode(&raw)
	return raw, err
}

// Load reads legacy TOML settings and applies nonzero/nonempty options.
func Load(o Options) (Settings, error) {
	var s Settings
	path := o.ConfigPath
	if path == "" {
		dir, err := xdgDir("XDG_CONFIG_HOME", ".config")
		if err != nil {
			return s, err
		}
		path = filepath.Join(dir, "config.toml")
	} else if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return s, err
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	raw, err := readConfig(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if o.ConfigPath != "" {
				return s, errors.New("specified configuration file was not found")
			}
		} else {
			return s, errors.New("cannot read configuration file; check permissions and TOML syntax")
		}
	}
	return parseConfig(raw, o)
}

// configReader keeps the first schema error and uses only known field labels in errors.
type configReader struct{ err error }

func (r *configReader) reject(label, why string) {
	if r.err == nil {
		r.err = fmt.Errorf("%s: %s", label, why)
	}
}

func (r *configReader) table(v any, label string, keys ...string) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		r.reject(label, "expected a table")
		return nil
	}
	if len(keys) != 0 {
		for name := range m {
			allowed := false
			for _, k := range keys {
				if k == name {
					allowed = true
					break
				}
			}
			if !allowed {
				r.reject(label, "unknown field; do not put credentials in the configuration")
				break
			}
		}
	}
	return m
}

func fallback(m map[string]any, key string, def any) any {
	if v, ok := m[key]; ok {
		return v
	}
	return def
}

func (r *configReader) text(v any, label string, nonblank bool) string {
	s, ok := v.(string)
	if !ok || strings.ContainsRune(s, 0) || nonblank && strings.TrimSpace(s) == "" {
		r.reject(label, "expected a valid string")
		return ""
	}
	return s
}

func (r *configReader) boolean(v any, label string) bool {
	b, ok := v.(bool)
	if !ok {
		r.reject(label, "expected true or false")
	}
	return b
}

func (r *configReader) integer(v any, low, high int64, label string) int {
	n, ok := v.(int64)
	if !ok || n < low || n > high {
		r.reject(label, fmt.Sprintf("expected an integer between %d and %d", low, high))
		return 0
	}
	return int(n)
}

func (r *configReader) price(v any) *float64 {
	n, ok := v.(float64)
	if !ok {
		i, integer := v.(int64)
		if !integer {
			r.reject("model price", "expected a number")
			return nil
		}
		n = float64(i)
	}
	if !(n >= 0 && n <= 1000000) {
		r.reject("model price", "expected a finite number between 0 and 1000000")
		return nil
	}
	return &n
}

func prefer[T comparable](override T, configured any) any {
	var zero T
	if override != zero {
		return override
	}
	return configured
}

func parseConfig(raw map[string]any, o Options) (Settings, error) {
	var r configReader
	var s Settings
	r.table(raw, "configuration", "aws", "openai", "chat", "history", "images", "models", "ui")
	table := func(name string, keys ...string) map[string]any {
		return r.table(fallback(raw, name, map[string]any{}), "["+name+"]", keys...)
	}
	aws := table("aws", "region", "profile", "model_id")
	openai := table("openai", "endpoint", "model_id", "stream", "response_timeout")
	conversation := table("chat", "provider", "max_input_tokens", "max_output_tokens", "system_prompt", "thinking")
	s.Thinking = chat.Thinking(r.text(prefer(o.Thinking, fallback(conversation, "thinking", "auto")), "chat.thinking", true))
	if !s.Thinking.Valid() {
		r.reject("chat.thinking", "expected auto, on, or off")
	}
	history := table("history", "enabled", "save_images")
	images := table("images", "max_edge", "max_bytes")
	ui := table("ui", "language")
	s.Models = make(map[string]ModelInfo)
	seen := make(map[string]bool)
	for alias, v := range table("models") {
		if !aliasRE.MatchString(alias) {
			r.reject("model alias", "expected 1–64 letters, digits, dots, underscores, or hyphens")
		}
		m := r.table(v, "model", "id", "vision", "input_per_million", "output_per_million")
		info := ModelInfo{ID: r.text(m["id"], "model ID", true)}
		if !validModel(info.ID) {
			r.reject("model ID", "invalid model ID or ARN")
		}
		if seen[info.ID] {
			r.reject("models", "different aliases cannot have the same model ID")
		}
		seen[info.ID] = true
		if v, ok := m["vision"]; ok {
			b := r.boolean(v, "vision")
			info.Vision = &b
		}
		in, out := m["input_per_million"], m["output_per_million"]
		if (in == nil) != (out == nil) {
			r.reject("model price", "input and output prices must be set together")
		}
		if in != nil && out != nil {
			info.InputPerMillion, info.OutputPerMillion = r.price(in), r.price(out)
		}
		s.Models[alias] = info
	}
	s.Language = r.text(fallback(ui, "language", "zh"), "ui.language", true)
	if !validLanguage(s.Language) || o.Language != "" && !validLanguage(o.Language) {
		r.reject("language", "expected zh, en, or ja")
	}
	if o.Language != "" {
		s.Language = o.Language
	}
	s.Provider = r.text(prefer(o.Provider, fallback(conversation, "provider", "bedrock")), "chat.provider", true)
	if s.Provider != "bedrock" && s.Provider != "openai" {
		r.reject("provider", "expected bedrock or openai")
	}
	if o.Endpoint != "" && s.Provider != "openai" {
		r.reject("--endpoint", "requires provider openai")
	}
	endpoint := r.text(prefer(o.Endpoint, fallback(openai, "endpoint", "http://localhost:11434/v1/chat/completions")), "openai.endpoint", true)
	var err error
	s.Endpoint, err = canonicalEndpoint(endpoint)
	if err != nil {
		r.reject("openai.endpoint", err.Error())
	}
	s.Stream = r.boolean(fallback(openai, "stream", true), "openai.stream")
	if o.ResponseTimeout != "" && s.Provider != "openai" {
		r.reject("--response-timeout", "requires provider openai")
	}
	timeout := r.text(prefer(o.ResponseTimeout, fallback(openai, "response_timeout", chat.DefaultOpenAIReadTimeout.String())), "openai.response_timeout", true)
	s.ResponseTimeout, err = time.ParseDuration(timeout)
	if err != nil || s.ResponseTimeout < time.Second || s.ResponseTimeout > time.Hour {
		r.reject("openai.response_timeout", "expected a duration from 1s to 1h, for example 5m")
	}
	backend := aws
	if s.Provider == "openai" {
		backend = openai
	}
	if v := prefer(o.Model, backend["model_id"]); v != nil {
		s.Model, err = s.ResolveModel(r.text(v, "model_id", true))
		if err != nil {
			r.reject("model_id", err.Error())
		}
	}
	if v := prefer(o.Region, aws["region"]); v != nil {
		s.Region = r.text(v, "aws.region", true)
		if !regionRE.MatchString(s.Region) {
			r.reject("aws.region", "invalid region")
		}
	}
	if v := prefer(o.Profile, aws["profile"]); v != nil {
		s.Profile = r.text(v, "aws.profile", true)
	}
	s.MaxInput = r.integer(prefer(int64(o.MaxInput), fallback(conversation, "max_input_tokens", int64(6000))), 64, 4000000, "max_input_tokens")
	s.MaxOutput = r.integer(prefer(int64(o.MaxOutput), fallback(conversation, "max_output_tokens", int64(1024))), 1, 256000, "max_output_tokens")
	s.System = r.text(fallback(conversation, "system_prompt", ""), "system_prompt", false)
	s.MaxEdge = r.integer(fallback(images, "max_edge", int64(1568)), 64, 8000, "images.max_edge")
	s.MaxBytes = r.integer(fallback(images, "max_bytes", int64(1500000)), 1024, 3750000, "images.max_bytes")
	s.History = r.boolean(fallback(history, "enabled", true), "history.enabled") && !o.NoHistory
	s.SaveImages = r.boolean(fallback(history, "save_images", false), "history.save_images") && s.History && !o.NoSaveImages
	if r.err != nil {
		return Settings{}, r.err
	}
	s.StateDir, err = xdgDir("XDG_STATE_HOME", ".local/state")
	if err != nil {
		return Settings{}, err
	}
	s.ConversationDir = s.StateDir
	if s.Provider == "openai" {
		scope := sha256.Sum256([]byte(s.Endpoint))
		s.ConversationDir += fmt.Sprintf("-openai-%x", scope[:12])
	}
	return s, nil
}
