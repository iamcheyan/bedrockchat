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
)

func decode(data []byte, format string) (image.Image, error) {
	var img image.Image
	var exif []byte
	var err error
	if format == "png" {
		exif, err = pngMetadata(data)
		if err != nil {
			return nil, err
		}
		img, err = png.Decode(bytes.NewReader(data))
	} else {
		exif = jpegEXIF(data)
		img, err = jpeg.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return nil, errInvalidImage
	}
	return orient(img, orientation(exif)), nil
}

// The standard PNG decoder ignores animation and metadata chunks. Walk the
// container to reject APNG and verify even the ancillary chunk checksums.
func pngMetadata(data []byte) ([]byte, error) {
	var exif []byte
	for data = data[8:]; len(data) >= 12; {
		size := binary.BigEndian.Uint32(data[:4])
		if uint64(size) > uint64(len(data)-12) {
			break
		}
		n := int(size) + 8
		if crc32.ChecksumIEEE(data[4:n]) != binary.BigEndian.Uint32(data[n:n+4]) {
			break
		}
		switch string(data[4:8]) {
		case "acTL", "fcTL", "fdAT":
			return nil, errors.New("animated PNG images are not supported")
		case "eXIf":
			exif = data[8:n]
		case "IEND":
			if size == 0 {
				return exif, nil
			}
			return nil, errInvalidImage
		}
		data = data[n+4:]
	}
	return nil, errInvalidImage
}

func jpegEXIF(data []byte) []byte {
	data = data[2:]
	for len(data) > 1 && data[0] == 0xff {
		marker := data[1]
		if marker == 0xff { // marker fill byte
			data = data[1:]
			continue
		}
		if marker == 0xda || marker == 0xd9 || len(data) < 4 {
			break
		}
		size := int(binary.BigEndian.Uint16(data[2:4]))
		if size < 2 || size > len(data)-2 {
			break
		}
		segment := data[4 : 2+size]
		if marker == 0xe1 && bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			return segment[6:]
		}
		data = data[2+size:]
	}
	return nil
}

// Read only the inline TIFF orientation tag in IFD0. All EXIF data is discarded
// on output; invalid or missing orientation means no transform.
func orientation(exif []byte) uint16 {
	if len(exif) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(exif[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	offset := order.Uint32(exif[4:8])
	if order.Uint16(exif[2:4]) != 42 || offset < 8 || uint64(offset) > uint64(len(exif)-2) {
		return 1
	}
	entries := exif[offset:]
	count := min(int(order.Uint16(entries[:2])), (len(entries)-2)/12)
	entries = entries[2:]
	for range count {
		if order.Uint16(entries[:2]) == 0x112 && order.Uint16(entries[2:4]) == 3 && order.Uint32(entries[4:8]) == 1 {
			return order.Uint16(entries[8:10])
		}
		entries = entries[12:]
	}
	return 1
}

func orient(img image.Image, rotation uint16) image.Image {
	if rotation < 2 || rotation > 8 {
		return img
	}
	return oriented{Image: img, rotation: rotation}
}

// A view avoids allocating a second full-size image just for rotation.
type oriented struct {
	image.Image
	rotation uint16
}

func (o oriented) Bounds() image.Rectangle {
	size := o.Image.Bounds().Size()
	if o.rotation >= 5 {
		size.X, size.Y = size.Y, size.X
	}
	return image.Rectangle{Max: size}
}

func (o oriented) At(x, y int) color.Color {
	b := o.Image.Bounds()
	w, h := b.Dx()-1, b.Dy()-1
	switch o.rotation {
	case 2:
		x = w - x
	case 3:
		x, y = w-x, h-y
	case 4:
		y = h - y
	case 5:
		x, y = y, x
	case 6:
		x, y = y, h-x
	case 7:
		x, y = w-y, h-x
	case 8:
		x, y = w-y, x
	}
	return o.Image.At(b.Min.X+x, b.Min.Y+y)
}

func (o oriented) Opaque() bool {
	img, ok := o.Image.(interface{ Opaque() bool })
	return ok && img.Opaque()
}
