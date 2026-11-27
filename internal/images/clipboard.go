package images

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
	"unicode/utf16"

	"github.com/iamcheyan/bedrockchat/internal/chat"
)

const clipboardTimeout = 15 * time.Second

// Fixed script ported from images.py. Keep these limits in sync with the Go
// source limits. No file paths, settings or other caller data enter the script.
const clipboardScript = `
$ErrorActionPreference = 'Stop'
$maxBytes = 32MB
$maxPixels = 40000000
try {
    Add-Type -AssemblyName System.Windows.Forms
    Add-Type -AssemblyName System.Drawing
    $clipboard = [System.Windows.Forms.Clipboard]::GetDataObject()
    if ($null -eq $clipboard) { exit 10 }

    $stdout = [Console]::OpenStandardOutput()
    # Snipping Tool may provide PNG alongside, or instead of, a bitmap.
    foreach ($format in @('PNG', 'image/png')) {
        try { $png = $clipboard.GetData($format, $false) }
        catch { continue }
        if ($png -is [IO.MemoryStream]) {
            try {
                if ($png.Length -eq 0) { continue }
                if ($png.Length -gt $maxBytes) { exit 12 }
                $png.WriteTo($stdout)
                $stdout.Flush()
                exit 0
            } finally { $png.Dispose() }
        }
        if ($png -is [byte[]] -and $png.Length -gt 0) {
            if ($png.Length -gt $maxBytes) { exit 12 }
            $stdout.Write($png, 0, $png.Length)
            $stdout.Flush()
            exit 0
        }
    }

    $image = $clipboard.GetData([System.Windows.Forms.DataFormats]::Bitmap, $true)
    if ($image -isnot [System.Drawing.Image]) { exit 10 }
    try {
        if ([long]$image.Width * $image.Height -gt $maxPixels) { exit 12 }
        $buffer = New-Object IO.MemoryStream
        try {
            $image.Save($buffer, [System.Drawing.Imaging.ImageFormat]::Png)
            if ($buffer.Length -gt $maxBytes) { exit 12 }
            $buffer.WriteTo($stdout)
            $stdout.Flush()
        } finally { $buffer.Dispose() }
    } finally { $image.Dispose() }
} catch { exit 11 }
exit 0
`

// Paste explicitly reads a Windows clipboard image via WSL interop. It does not
// query clipboard text. ctx can cancel the PowerShell read before its deadline.
func Paste(ctx context.Context, maxEdge, maxBytes int) (chat.Image, error) {
	if err := checkLimits(maxEdge, maxBytes); err != nil {
		return chat.Image{}, err
	}
	data, err := readClipboard(ctx)
	if err != nil {
		return chat.Image{}, err
	}
	return prepare(data, "clipboard.png", true, maxEdge, maxBytes)
}

func readClipboard(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, clipboardTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, clipboardContextError(err)
	}
	var encoded []byte
	for _, code := range utf16.Encode([]rune(clipboardScript)) {
		encoded = binary.LittleEndian.AppendUint16(encoded, code)
	}
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-STA",
		"-EncodedCommand", base64.StdEncoding.EncodeToString(encoded))
	stdout, writeEnd, err := os.Pipe()
	if err != nil {
		return nil, errors.New("could not read Windows clipboard")
	}
	defer stdout.Close()
	cmd.Stdout = writeEnd // stdin and stderr default to the null device.
	err = cmd.Start()
	writeEnd.Close()
	if err != nil {
		if ctx.Err() != nil {
			return nil, clipboardContextError(ctx.Err())
		}
		return nil, errors.New("could not start Windows PowerShell; check WSL interop")
	}
	// Close our pipe on cancellation even if an inherited writer still holds it.
	stop := context.AfterFunc(ctx, func() { stdout.Close() })
	defer stop()
	data, readErr := io.ReadAll(io.LimitReader(stdout, maxInputBytes+1))
	if len(data) > maxInputBytes || readErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if len(data) > maxInputBytes {
		return nil, errors.New("clipboard image is too large")
	}
	if ctx.Err() != nil {
		return nil, clipboardContextError(ctx.Err())
	}
	if readErr != nil {
		return nil, errors.New("could not read Windows clipboard")
	}
	if waitErr != nil {
		var status *exec.ExitError
		if errors.As(waitErr, &status) {
			switch status.ExitCode() {
			case 10:
				return nil, errors.New("Windows clipboard does not contain an image")
			case 12:
				return nil, errors.New("clipboard image is too large")
			}
		}
		return nil, errors.New("could not read Windows clipboard; try again")
	}
	if len(data) == 0 {
		return nil, errors.New("Windows clipboard does not contain an image")
	}
	return data, nil
}

func clipboardContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("reading Windows clipboard timed out: %w", err)
	}
	return fmt.Errorf("reading Windows clipboard: %w", err)
}
