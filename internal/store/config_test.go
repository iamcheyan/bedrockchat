package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSavedRuntimeChoicesOverrideDefaultsButNotFlags(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	initial, err := Load(Options{})
	if err != nil {
		t.Fatal(err)
	}
	prefs := Store{Root: initial.StateDir, Enabled: true}
	if err := prefs.UpdateRuntimePreferences(func(p *RuntimePreferences) error {
		p.Provider = "openai"
		p.Thinking = "off"
		p.ResponseTimeout = "10m"
		p.MaxOutput = 2048
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	saved, err := Load(Options{})
	if err != nil || saved.Provider != "openai" || saved.Thinking != "off" || saved.ResponseTimeout != 10*time.Minute || saved.MaxOutput != 2048 {
		t.Fatalf("saved state: %+v, %v", saved, err)
	}
	explicit, err := Load(Options{Thinking: "on", MaxOutput: 512})
	if err != nil || explicit.Thinking != "on" || explicit.MaxOutput != 512 {
		t.Fatalf("explicit state: %+v, %v", explicit, err)
	}
	private, err := Load(Options{NoHistory: true})
	if err != nil || private.Provider != "bedrock" || private.Thinking != "auto" || private.MaxOutput != 1024 {
		t.Fatalf("private state: %+v, %v", private, err)
	}
}

func TestOpenAIResponseTimeoutConfigAndOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	for _, tc := range []struct {
		config, override string
		want             time.Duration
	}{
		{"", "", 5 * time.Minute},
		{"[openai]\nresponse_timeout = '8m'", "", 8 * time.Minute},
		{"[openai]\nresponse_timeout = '8m'", "10m", 10 * time.Minute},
		{"[openai]\nresponse_timeout = 90", "", 0},
		{"", "0s", 0},
		{"", "2h", 0},
	} {
		writeTestFile(t, path, tc.config)
		got, err := Load(Options{ConfigPath: path, Provider: "openai", ResponseTimeout: tc.override})
		if tc.want == 0 {
			if err == nil || !strings.Contains(err.Error(), "openai.response_timeout") {
				t.Fatalf("expected timeout validation error, got %v", err)
			}
		} else if err != nil || got.ResponseTimeout != tc.want {
			t.Fatalf("timeout = %v, want %v; error = %v", got.ResponseTimeout, tc.want, err)
		}
	}
}

func TestThinkingConfigAndOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	for _, tc := range []struct{ config, override, want string }{
		{"", "", "auto"},
		{"[chat]\nthinking = 'off'", "", "off"},
		{"[chat]\nthinking = 'on'", "off", "off"},
		{"[chat]\nthinking = 'invalid'", "", ""},
		{"", "invalid", ""},
	} {
		writeTestFile(t, path, tc.config)
		got, err := Load(Options{ConfigPath: path, Thinking: tc.override})
		if tc.want == "" {
			if err == nil {
				t.Fatal("accepted invalid thinking setting")
			}
		} else if err != nil || string(got.Thinking) != tc.want {
			t.Fatalf("thinking = %q, want %q: %v", got.Thinking, tc.want, err)
		}
	}
}

func TestLoadLegacyConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "relative-ignored")
	state := filepath.Join(home, "state")
	t.Setenv("XDG_STATE_HOME", state)
	path := filepath.Join(home, ".config", "bedrock-chat", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, `
[aws]
region = "us-east-1"
profile = "work"
model_id = "vision"
[chat]
provider = "openai"
max_input_tokens = 314
system_prompt = "hello"
[openai]
endpoint = "HTTPS://EXAMPLE.COM:0443/v1/chat/completions"
model_id = "vision"
stream = false
[models.vision]
id = "example/model:latest"
vision = false
input_per_million = 1
output_per_million = 2.5
[history]
save_images = true
[ui]
language = "ja"
`)
	got, err := Load(Options{Region: "us-west-2", MaxOutput: 200})
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "openai" || got.Endpoint != "https://example.com/v1/chat/completions" || got.Stream ||
		got.Region != "us-west-2" || got.Profile != "work" || got.Model != "example/model:latest" || got.Language != "ja" ||
		got.System != "hello" || got.MaxInput != 314 || got.MaxOutput != 200 || got.MaxEdge != 1568 || got.MaxBytes != 1500000 ||
		!got.History || !got.SaveImages || got.StateDir != filepath.Join(state, "bedrock-chat") ||
		got.ConversationDir != filepath.Join(state, "bedrock-chat-openai-23269077e6e4e6e463ef9118") {
		t.Fatalf("settings: %+v", got)
	}
	m := got.ModelInfo(got.Model)
	if m.ID != got.Model || m.Vision == nil || *m.Vision || m.InputPerMillion == nil || *m.InputPerMillion != 1 || m.OutputPerMillion == nil || *m.OutputPerMillion != 2.5 {
		t.Fatalf("model: %+v", m)
	}
	if id, err := got.ResolveModel("vision"); id != got.Model || err != nil {
		t.Fatalf("alias: %q, %v", id, err)
	}
	if _, err := got.ResolveModel("arn:aws:bedrock:us-east-1:1:prompt/foo"); err == nil {
		t.Fatal("accepted prompt ARN")
	}
	if m := got.ModelInfo("unknown"); m.ID != "unknown" || m.Vision != nil {
		t.Fatalf("unknown model: %+v", m)
	}
}

func TestLoadRejectsInvalidSettingsWithoutLeakingValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.toml")
	for _, text := range []string{
		"[aws]\napi_key = 'DO_NOT_PRINT'", "[chat]\nmax_input_tokens = true", "[history]\nsave_images = 1",
		"[images]\nmax_bytes = 3999999", "[models.bad]\nid = 'a'\ninput_per_million = 1",
		"[models.bad]\nid = 'a'\ninput_per_million = inf\noutput_per_million = 1", "[aws]\nregion = 'us-east-1.evil.example'",
		"[ui]\nlanguage = 'fr'", "[openai]\nendpoint = 'https://DO_NOT_PRINT@example.com/v1/chat/completions'",
		"[openai]\nendpoint = 'http://example.com/v1/chat/completions'", "[models.one]\nid = 'a'\n[models.two]\nid = 'a'",
		"[aws]\nregion = 'DO_NOT_PRINT", "[aws]\nprofile = '''   '''", "[models]\nbad = []",
	} {
		writeTestFile(t, path, text)
		if _, err := Load(Options{ConfigPath: path}); err == nil || strings.Contains(err.Error(), "DO_NOT_PRINT") {
			t.Fatalf("expected sanitized error, got %v for fixture %q", err, text)
		}
	}
	if _, err := Load(Options{ConfigPath: path + ".missing"}); err == nil {
		t.Fatal("explicit missing config accepted")
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}
