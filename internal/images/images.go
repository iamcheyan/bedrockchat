// Package images prepares explicitly requested local and clipboard attachments.
package images

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/iamcheyan/bedrockchat/internal/chat"
	"golang.org/x/image/draw"
	"golang.org/x/sys/unix"
)

const (
	maxInputBytes  = 32 * 1024 * 1024
	maxInputPixels = 40_000_000
	maxOutputBytes = 3_750_000
	maxOutputEdge  = 8000
)

var (
	errInvalidImage = errors.New("could not decode image; use a valid PNG or JPEG")
	errInputPixels  = errors.New("source image exceeds the pixel limit")
)

// Read reads a regular local PNG or JPEG file, stripping metadata and fitting the limits.
// maxEdge and maxBytes must be positive and within Bedrock's image limits.
func Read(name string, maxEdge, maxBytes int) (chat.Image, error) {
	if err := checkLimits(maxEdge, maxBytes); err != nil {
		return chat.Image{}, err
	}
	// Nonblocking open prevents a FIFO from hanging before we can reject it.
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return chat.Image{}, errors.New("could not read image file")
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return chat.Image{}, errors.New("could not read image file")
	}
	if !info.Mode().IsRegular() {
		return chat.Image{}, errors.New("image path must refer to a regular file")
	}
	if info.Size() > maxInputBytes {
		return chat.Image{}, errors.New("source image file is too large")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxInputBytes+1))
	if err != nil {
		return chat.Image{}, errors.New("could not read image file")
	}
	if len(data) > maxInputBytes {
		return chat.Image{}, errors.New("source image file is too large")
	}
	return prepare(data, filepath.Base(name), false, maxEdge, maxBytes)
}

func checkLimits(maxEdge, maxBytes int) error {
	if maxEdge < 1 || maxEdge > maxOutputEdge {
		return fmt.Errorf("image edge limit must be 1–%d pixels", maxOutputEdge)
	}
	if maxBytes < 1 || maxBytes > maxOutputBytes {
		return fmt.Errorf("image byte limit must be 1–%d bytes", maxOutputBytes)
	}
	return nil
}

func prepare(data []byte, name string, pngOnly bool, maxEdge, maxBytes int) (chat.Image, error) {
	reader := bytes.NewReader(data)
	var config image.Config
	var format string
	var err error
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		format = "png"
		config, err = png.DecodeConfig(reader)
	case !pngOnly && bytes.HasPrefix(data, []byte("\xff\xd8")):
		format = "jpeg"
		config, err = jpeg.DecodeConfig(reader)
	default:
		return chat.Image{}, errInvalidImage
	}
	if err != nil {
		return chat.Image{}, errInvalidImage
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxInputPixels/config.Height {
		return chat.Image{}, errInputPixels
	}
	img, err := decode(data, format)
	if err != nil {
		return chat.Image{}, err
	}
	encoded, size, err := fit(img, format, maxEdge, maxBytes)
	if err != nil {
		return chat.Image{}, err
	}
	return chat.Image{Data: encoded, Format: format, Width: size.X, Height: size.Y, Name: safeName(name, format)}, nil
}

func fit(img image.Image, format string, maxEdge, maxBytes int) ([]byte, image.Point, error) {
	img = thumbnail(img, maxEdge)
	var encoder png.Encoder
	encoder.CompressionLevel = png.BestCompression
	var out measuredBuffer
	out.limit = maxBytes
	for {
		out.Reset()
		out.size = 0
		var err error
		if format == "png" {
			err = encoder.Encode(&out, img)
		} else {
			err = jpeg.Encode(&out, img, &jpeg.Options{Quality: 92})
		}
		if err != nil {
			return nil, image.Point{}, errors.New("could not encode image")
		}
		size := img.Bounds().Size()
		if out.size <= maxBytes {
			return out.Bytes(), size, nil
		}
		edge := max(size.X, size.Y)
		if edge == 1 {
			return nil, image.Point{}, errors.New("image cannot fit the byte limit")
		}
		ratio := math.Sqrt(float64(maxBytes) / float64(out.size))
		next := min(edge-1, max(1, int(float64(edge)*max(0.5, min(0.95, ratio)))))
		img = thumbnail(img, next)
	}
}

// Count the full encoding to inform the next reduction, but retain only what could be sent.
type measuredBuffer struct {
	bytes.Buffer
	limit, size int
}

func (b *measuredBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.size < b.limit {
		_, _ = b.Buffer.Write(p[:min(n, b.limit-b.size)])
	}
	b.size += n
	return n, nil
}

func thumbnail(img image.Image, edge int) image.Image {
	size := img.Bounds().Size()
	if size.X <= edge && size.Y <= edge {
		return img
	}
	if size.X >= size.Y {
		size = image.Pt(edge, max(1, (size.Y*edge+size.X/2)/size.X))
	} else {
		size = image.Pt(max(1, (size.X*edge+size.Y/2)/size.Y), edge)
	}
	// Catmull-Rom keeps screenshot text sharp. Its temporary buffer is
	// proportional to destWidth*sourceHeight, and kernel weights to source
	// width+height. Bound scratch memory for unusually large inputs.
	src := img.Bounds()
	var scaler draw.Scaler = draw.CatmullRom
	if size.X > 4_000_000/src.Dy() || max(src.Dx(), src.Dy()) > 65_536 {
		scaler = draw.ApproxBiLinear
	}
	dst := image.NewNRGBA(image.Rectangle{Max: size})
	scaler.Scale(dst, dst.Bounds(), img, src, draw.Src, nil)
	return dst
}

func safeName(name, format string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = path.Base(name)
	if len(name) >= 2 && name[1] == ':' && ((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z')) {
		name = name[2:]
	}
	if ext := path.Ext(name); len(ext) < len(name) {
		name = strings.TrimSuffix(name, ext)
	}
	name = strings.Trim(strings.Map(func(r rune) rune {
		if unicode.In(r, unicode.Categories["C"]) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(name, "")), " .")
	if name == "" {
		name = "image"
	}
	if format == "jpeg" {
		return name + ".jpg"
	}
	return name + ".png"
}
