// Package photos checks uploaded WebP and JPEG photos without decoding pixels.
package photos

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"

	"golang.org/x/image/webp"
)

var ErrContainer = errors.New("This photo is incomplete or is not a supported WebP or JPEG image.")
var ErrMetadata = errors.New("This photo still contains camera or location metadata. Choose it again to make a clean copy.")

// Validate returns the photo's header, its file extension ("webp" or "jpg"),
// and ErrContainer, ErrMetadata or a dimension error when it is refused.
func Validate(data []byte, maxDimension int) (image.Config, string, error) {
	var cfg image.Config
	var err error
	ext := ""
	switch {
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		ext = "webp"
		if err = CheckWebP(data); err == nil {
			cfg, err = webp.DecodeConfig(bytes.NewReader(data))
		}
	case len(data) >= 3 && bytes.Equal(data[:3], []byte{255, 216, 255}):
		ext = "jpg"
		if err = CheckJPEG(data); err == nil {
			cfg, err = jpeg.DecodeConfig(bytes.NewReader(data))
		}
	default:
		err = ErrContainer
	}
	if err != nil {
		if errors.Is(err, ErrMetadata) {
			return cfg, ext, err
		}
		return cfg, ext, ErrContainer
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxDimension || cfg.Height > maxDimension {
		return cfg, ext, fmt.Errorf("Photos must be no more than %d pixels on their longest side.", maxDimension)
	}
	return cfg, ext, nil
}
