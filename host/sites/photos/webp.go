package photos

import (
	"bytes"
	"encoding/binary"
	"image"
	"io"

	"golang.org/x/image/webp"
)

// webpFile is what the chunks read so far declare about the image.
type webpFile struct {
	imageSeen, extended, alpha, icc bool
	canvasWidth, canvasHeight       int
	flags                           byte
}

// CheckWebP walks all RIFF chunks, not just the first header read by DecodeConfig.
func CheckWebP(b []byte) error {
	if uint64(binary.LittleEndian.Uint32(b[4:8]))+8 != uint64(len(b)) {
		return ErrContainer
	}
	var f webpFile
	for off := 12; off < len(b); {
		kind, chunk, err := webpChunkAt(b, off)
		if err != nil {
			return err
		}
		if err := f.add(kind, chunk, off+8); err != nil {
			return err
		}
		off += 8 + len(chunk)
		if len(chunk)&1 != 0 {
			if off >= len(b) || b[off] != 0 {
				return ErrContainer
			}
			off++
		}
	}
	if !f.imageSeen || f.extended && (f.flags&0x20 != 0) != f.icc {
		return ErrContainer
	}
	return nil
}

func webpChunkAt(b []byte, off int) (string, []byte, error) {
	if len(b)-off < 8 {
		return "", nil, ErrContainer
	}
	kind := string(b[off : off+4])
	n := uint64(binary.LittleEndian.Uint32(b[off+4 : off+8]))
	off += 8
	if n > uint64(len(b)-off) {
		return "", nil, ErrContainer
	}
	return kind, b[off : off+int(n)], nil
}

func (f *webpFile) add(kind string, chunk []byte, off int) error {
	switch kind {
	case "EXIF", "XMP ":
		return ErrMetadata
	case "VP8X":
		return f.addHeader(chunk, off)
	case "VP8 ", "VP8L":
		return f.addImage(kind, chunk)
	case "ALPH":
		return f.addAlpha(chunk)
	case "ICCP":
		return f.addICC(chunk)
	}
	return ErrContainer // Includes animation chunks and extra payloads.
}

func (f *webpFile) addHeader(chunk []byte, off int) error {
	if off != 20 || f.extended || len(chunk) != 10 {
		return ErrContainer
	}
	f.extended = true
	f.flags = chunk[0]
	f.canvasWidth = 1 + int(chunk[4]) + int(chunk[5])<<8 + int(chunk[6])<<16
	f.canvasHeight = 1 + int(chunk[7]) + int(chunk[8])<<8 + int(chunk[9])<<16
	if chunk[0]&0x0c != 0 {
		return ErrMetadata
	}
	if chunk[0]&0xc3 != 0 || chunk[1] != 0 || chunk[2] != 0 || chunk[3] != 0 {
		return ErrContainer
	}
	return nil
}

func (f *webpFile) addImage(kind string, chunk []byte) error {
	if f.imageSeen || len(chunk) == 0 || kind == "VP8L" && f.alpha {
		return ErrContainer
	}
	f.imageSeen = true
	cfg, err := webpImageConfig(kind, chunk)
	if err != nil || f.extended && (cfg.Width != f.canvasWidth || cfg.Height != f.canvasHeight) {
		return ErrContainer
	}
	if f.extended && ((f.flags&0x10 != 0) != (f.alpha || kind == "VP8L" && chunk[4]&0x10 != 0)) {
		return ErrContainer
	}
	return nil
}

// webpImageConfig reads the header of the image chunk itself: DecodeConfig
// stops at VP8X in x/image, so a small canvas could hide a larger payload.
func webpImageConfig(kind string, chunk []byte) (image.Config, error) {
	if kind == "VP8 " && !vp8KeyframeFits(chunk) || kind == "VP8L" && (len(chunk) < 5 || chunk[0] != 0x2f) {
		return image.Config{}, ErrContainer
	}
	header := make([]byte, 20)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(chunk))+12)
	copy(header[8:], "WEBP"+kind)
	binary.LittleEndian.PutUint32(header[16:20], uint32(len(chunk)))
	return webp.DecodeConfig(io.MultiReader(bytes.NewReader(header), bytes.NewReader(chunk)))
}

// A VP8 keyframe's first partition must fit inside the chunk.
func vp8KeyframeFits(chunk []byte) bool {
	return len(chunk) >= 10 && chunk[0]&1 == 0 && int(chunk[0]>>5)|int(chunk[1])<<3|int(chunk[2])<<11 <= len(chunk)-10
}

func (f *webpFile) addAlpha(chunk []byte) error {
	if !f.extended || f.imageSeen || f.alpha || len(chunk) == 0 || chunk[0]&0xc3 > 1 {
		return ErrContainer
	}
	f.alpha = true
	if chunk[0]&3 == 0 && len(chunk) != 1+f.canvasWidth*f.canvasHeight {
		return ErrContainer
	}
	return nil
}

func (f *webpFile) addICC(chunk []byte) error {
	if !f.extended || f.imageSeen || f.alpha || f.icc || len(chunk) == 0 || len(chunk) > 16*1024 {
		return ErrContainer
	}
	f.icc = true
	return nil
}
