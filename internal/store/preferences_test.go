package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRuntimePreferencesMissingPartialAndPermissions(t *testing.T) {
	st := Store{Root: filepath.Join(t.TempDir(), "state"), Enabled: true}
	got, err := st.RuntimePreferences()
	if err != nil || got != (RuntimePreferences{}) {
		t.Fatalf("missing preferences = %#v, %v", got, err)
	}
	flag := true
	if err := st.UpdateRuntimePreferences(func(p *RuntimePreferences) error {
		p.Provider = "openai"
		p.SaveImages = &flag
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateRuntimePreferences(func(p *RuntimePreferences) error {
		p.MaxInput = 4096
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err = st.RuntimePreferences()
	if err != nil || got.Provider != "openai" || got.MaxInput != 4096 || got.SaveImages == nil || !*got.SaveImages {
		t.Fatalf("merged preferences = %#v, %v", got, err)
	}
	info, err := os.Stat(filepath.Join(st.Root, preferencesName))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("preference mode = %v, %v", info, err)
	}
	lockInfo, err := os.Stat(filepath.Join(st.Root, ".preferences.lock"))
	if err != nil || lockInfo.Mode().Perm() != 0600 {
		t.Fatalf("lock mode = %v, %v", lockInfo, err)
	}
}

func TestRuntimePreferencesDisabledDoesNotTouchDisk(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	st := Store{Root: root, Enabled: false}
	called := false
	got, err := st.RuntimePreferences()
	if err != nil || got != (RuntimePreferences{}) {
		t.Fatalf("read disabled store = %#v, %v", got, err)
	}
	if err := st.UpdateRuntimePreferences(func(*RuntimePreferences) error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("disabled update callback ran")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("disabled store touched disk: %v", err)
	}
}

func TestRuntimePreferencesRejectCorruptionUnknownFieldsAndSecrets(t *testing.T) {
	for name, data := range map[string]string{
		"malformed":     `{`,
		"unknown":       `{"provider":"bedrock","model":"secret-model"}`,
		"invalid-value": `{"provider":"secret-provider"}`,
		"oversized":     `{"profile":"` + strings.Repeat("x", maxPreferencesBytes) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			st := Store{Root: filepath.Join(t.TempDir(), "state"), Enabled: true}
			if err := os.MkdirAll(st.Root, 0700); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(st.Root, preferencesName), data)
			if _, err := st.RuntimePreferences(); err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "model") {
				t.Fatalf("unsafe error: %v", err)
			}
		})
	}
}

func TestRuntimePreferencesRejectSymlinkAndNonRegularFile(t *testing.T) {
	for _, kind := range []string{"symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			st := Store{Root: filepath.Join(root, "state"), Enabled: true}
			if err := os.Mkdir(st.Root, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(st.Root, preferencesName)
			if kind == "symlink" {
				target := filepath.Join(root, "outside")
				writeTestFile(t, target, `{"provider":"openai"}`)
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := st.RuntimePreferences(); err == nil {
				t.Fatal("accepted unsafe preference file")
			}
			if err := st.UpdateRuntimePreferences(func(p *RuntimePreferences) error { p.Provider = "openai"; return nil }); err == nil {
				t.Fatal("replaced unsafe preference file")
			}
		})
	}
}

func TestRuntimePreferencesConcurrentUpdatesDoNotLoseFields(t *testing.T) {
	st := Store{Root: filepath.Join(t.TempDir(), "state"), Enabled: true}
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Go(func() {
			key := ""
			switch i {
			case 0:
				key = "provider"
			case 1:
				key = "endpoint"
			case 2:
				key = "region"
			case 3:
				key = "profile"
			case 4:
				key = "thinking"
			case 5:
				key = "timeout"
			case 6:
				key = "input"
			case 7:
				key = "output"
			}
			if err := st.UpdateRuntimePreferences(func(p *RuntimePreferences) error {
				switch key {
				case "provider":
					p.Provider = "bedrock"
				case "endpoint":
					p.Endpoint = "https://example.com/v1/chat/completions"
				case "region":
					p.Region = "us-east-1"
				case "profile":
					p.Profile = "work"
				case "thinking":
					p.Thinking = "auto"
				case "timeout":
					p.ResponseTimeout = "5m"
				case "input":
					p.MaxInput = 4096
				case "output":
					p.MaxOutput = 512
				}
				return nil
			}); err != nil {
				t.Errorf("update %s: %v", key, err)
			}
		})
	}
	wg.Wait()
	got, err := st.RuntimePreferences()
	if err != nil || got.Provider != "bedrock" || got.Endpoint == "" || got.Region == "" || got.Profile == "" || got.Thinking == "" || got.ResponseTimeout == "" || got.MaxInput == 0 || got.MaxOutput == 0 {
		t.Fatalf("concurrent updates lost data: %#v, %v", got, err)
	}
}
