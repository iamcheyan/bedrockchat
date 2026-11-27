package images

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func fakePowerShell(t *testing.T, body string) {
	t.Helper()
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(bin, "powershell.exe"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
}

func TestPasteUsesFixedSTACommandAndPNG(t *testing.T) {
	path := writeImage(t, "shot", pngBytes(t, image.NewRGBA(image.Rect(0, 0, 180, 120))))
	argsPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("TEST_POWERSHELL_ARGS", argsPath)
	t.Setenv("TEST_CLIPBOARD_PNG", path)
	fakePowerShell(t, `printf '%s\n' "$@" > "$TEST_POWERSHELL_ARGS"; exec /bin/cat "$TEST_CLIPBOARD_PNG"`)
	result, err := Paste(t.Context(), 90, 2000)
	if err != nil || result.Name != "clipboard.png" || result.Format != "png" || result.Width != 90 || result.Height != 60 {
		t.Fatalf("image %s %s %dx%d, error %v", result.Name, result.Format, result.Width, result.Height, err)
	}
	argv, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	flags := "-NoLogo\n-NoProfile\n-NonInteractive\n-STA\n-EncodedCommand\n"
	if !strings.HasPrefix(string(argv), flags) {
		t.Fatalf("wrong PowerShell flags: %q", bytes.SplitN(argv, []byte("-EncodedCommand"), 2)[0])
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(argv[len(flags):])))
	if err != nil || len(decoded)%2 != 0 {
		t.Fatalf("invalid encoded command: %v", err)
	}
	cmd := make([]uint16, len(decoded)/2)
	for i := range cmd {
		cmd[i] = binary.LittleEndian.Uint16(decoded[2*i:])
	}
	script := string(utf16.Decode(cmd))
	png := strings.Index(script, "@('PNG', 'image/png')")
	bitmap := strings.Index(script, "DataFormats]::Bitmap")
	if png < 0 || bitmap <= png || !strings.Contains(script, "[Console]::OpenStandardOutput()") ||
		strings.Contains(script, "GetText") || strings.Contains(script, "Get-Clipboard") {
		t.Fatal("clipboard format selection differs from Python")
	}
}

func TestPasteReportsClipboardStatusWithoutLeakingOutput(t *testing.T) {
	for _, tc := range []struct{ command, want string }{
		{"exit 0", "does not contain an image"},
		{"exit 10", "does not contain an image"},
		{"exit 11", "could not read"},
		{"exit 12", "too large"},
		{"exit 1", "could not read"},
		{"exit 0 # output below", "valid PNG"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			body := ""
			if tc.command != "exit 0" {
				body = "printf 'private data'\n"
			}
			fakePowerShell(t, body+tc.command)
			_, err := Paste(t.Context(), 90, 2000)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private data") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestPasteRejectsJPEG(t *testing.T) {
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 8, 6)), nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_CLIPBOARD_JPEG", writeImage(t, "shot", data.Bytes()))
	fakePowerShell(t, `exec /bin/cat "$TEST_CLIPBOARD_JPEG"`)
	if _, err := Paste(t.Context(), 90, 2000); !errors.Is(err, errInvalidImage) {
		t.Fatalf("unexpected JPEG clipboard error: %v", err)
	}
}

func TestPasteDeadlineWithPartialWrite(t *testing.T) {
	fakePowerShell(t, "printf x; exec /bin/sleep 30")
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Paste(ctx, 90, 2000)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("got %v after %v", err, time.Since(start))
	}
}

func TestPasteStopsOversizeStdoutWithoutWaitingForExit(t *testing.T) {
	fakePowerShell(t, "exec /usr/bin/yes")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := Paste(ctx, 90, 2000); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPasteDoesNotLaunchOnInvalidLimitsOrCanceledContext(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv("TEST_POWERSHELL_MARKER", marker)
	fakePowerShell(t, `: > "$TEST_POWERSHELL_MARKER"`)
	if _, err := Paste(t.Context(), 0, 2000); err == nil {
		t.Fatal("accepted invalid edge limit")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Paste(ctx, 90, 2000); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("PowerShell ran: %v", err)
	}
}
