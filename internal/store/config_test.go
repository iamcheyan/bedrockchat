package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
