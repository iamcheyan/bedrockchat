package images

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func writeImage(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadResizesKeepsPNGAndAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 320, 160))
	for y := range 160 {
		for x := range 320 {
			img.SetNRGBA(x, y, color.NRGBA{G: 128, B: 255, A: 100})
		}
	}
	source := pngBytes(t, img)
	metadata := pngChunk("tEXt", []byte("Comment\x00private source path"))
	source = bytes.Join([][]byte{source[:33], metadata, source[33:]}, nil)
	result, err := Read(writeImage(t, "C:\\private\\截图\x1b.jpg", source), 100, 1500)
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "截图.png" || result.Format != "png" || result.Width != 100 || result.Height != 50 {
		t.Fatalf("unexpected result: %s %s %dx%d", result.Name, result.Format, result.Width, result.Height)
	}
	if bytes.Contains(result.Data, []byte("private source path")) {
		t.Fatal("PNG retained source metadata")
	}
	decoded, err := png.Decode(bytes.NewReader(result.Data))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, alpha := decoded.At(0, 0).RGBA()
	if alpha/257 != 100 || decoded.Bounds().Size() != image.Pt(100, 50) {
		t.Fatalf("output bounds %v, alpha %d", decoded.Bounds(), alpha)
	}
}

func TestReadKeepsPNGWithinByteLimit(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 500, 250))
	rng := rand.New(rand.NewPCG(42, 5))
	for i := range img.Pix {
		img.Pix[i] = byte(rng.Uint32())
	}
	path := writeImage(t, "noise.png", pngBytes(t, img))
	got, err := Read(path, 500, 12_000)
	if err != nil || got.Format != "png" || len(got.Data) > 12_000 || got.Width >= 500 || got.Width < 64 || abs(got.Width-2*got.Height) > 3 {
		t.Fatalf("got %s %dx%d (%d bytes), %v", got.Format, got.Width, got.Height, len(got.Data), err)
	}
	if _, err := Read(path, 500, 1); err == nil {
		t.Fatal("accepted an impossible byte limit")
	}
}

func abs(n int) int { return max(n, -n) }

func TestReadRejectsMalformedAndAnimatedPNG(t *testing.T) {
	data := pngBytes(t, image.NewGray(image.Rect(0, 0, 2, 2)))
	animated := bytes.Join([][]byte{data[:33], pngChunk("acTL", []byte{0, 0, 0, 2, 0, 0, 0, 0}), data[33:]}, nil)
	badCRC := pngChunk("tEXt", []byte("Comment\x00private"))
	badCRC[len(badCRC)-1] ^= 1
	for name, bad := range map[string][]byte{
		"animation": animated, "missing IEND": data[:len(data)-12], "garbage": []byte("nope"),
		"bad CRC": bytes.Join([][]byte{data[:33], badCRC, data[33:]}, nil),
	} {
		t.Run(name, func(t *testing.T) {
			path := writeImage(t, "private.png", bad)
			if _, err := Read(path, 100, 2000); err == nil || strings.Contains(err.Error(), path) {
				t.Fatalf("unexpected error %v", err)
			}
		})
	}
}

func TestReadChecksSourceLimitsBeforeDecode(t *testing.T) {
	data := pngBytes(t, image.NewGray(image.Rect(0, 0, 2, 2)))
	header := bytes.Clone(data[16:29])
	binary.BigEndian.PutUint32(header[:4], 7000)
	binary.BigEndian.PutUint32(header[4:8], 7000)
	path := writeImage(t, "giant.png", bytes.Join([][]byte{data[:8], pngChunk("IHDR", header), data[33:]}, nil))
	if _, err := Read(path, 100, 2000); !errors.Is(err, errInputPixels) {
		t.Fatalf("unexpected pixel limit error: %v", err)
	}
	if err := os.Truncate(path, maxInputBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path, 100, 2000); err == nil || !strings.Contains(err.Error(), "file is too large") {
		t.Fatalf("unexpected input byte limit error: %v", err)
	}
}

func TestReadRejectsFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	if _, err := Read(path, 100, 2000); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("unexpected FIFO result: %v", err)
	}
}

func TestReadRotatesJPEGAndStripsEXIF(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 80, 40))
	for y := range 40 {
		for x := range 80 {
			if x < 40 {
				img.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{B: 255, A: 255})
			}
		}
	}
	var source bytes.Buffer
	if err := jpeg.Encode(&source, img, nil); err != nil {
		t.Fatal(err)
	}
	exif := []byte("Exif\x00\x00II\x2a\x00\x08\x00\x00\x00\x01\x00\x12\x01\x03\x00\x01\x00\x00\x00\x06\x00\x00\x00\x00\x00\x00\x00")
	segment := binary.BigEndian.AppendUint16([]byte{0xff, 0xe1}, uint16(len(exif)+2))
	data := bytes.Join([][]byte{source.Bytes()[:2], segment, exif, source.Bytes()[2:]}, nil)
	got, err := Read(writeImage(t, "oriented.png", data), 100, 10_000)
	if err != nil || got.Width != 40 || got.Height != 80 || got.Format != "jpeg" || got.Name != "oriented.jpg" || bytes.Contains(got.Data, []byte("Exif")) {
		t.Fatalf("got %s %dx%d, %v", got.Format, got.Width, got.Height, err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(got.Data))
	if err != nil {
		t.Fatal(err)
	}
	r, _, _, _ := decoded.At(20, 10).RGBA()
	_, _, b, _ := decoded.At(20, 70).RGBA()
	if r < 0xf000 || b < 0xf000 {
		t.Fatalf("EXIF rotation did not put red above blue: red=%x blue=%x", r, b)
	}
}

func pngChunk(kind string, payload []byte) []byte {
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(payload)))
	chunk = append(chunk, kind...)
	chunk = append(chunk, payload...)
	return binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
}
