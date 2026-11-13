package hackhttp

import (
	"hash/crc32"
)

// pngOfSize is a PNG header declaring w x h (enough for DecodeConfig).
func pngOfSize(w, h int) []byte {
	ihdr := []byte{'I', 'H', 'D', 'R', byte(w >> 24), byte(w >> 16), byte(w >> 8), byte(w), byte(h >> 24), byte(h >> 16), byte(h >> 8), byte(h), 8, 2, 0, 0, 0}
	c := crc32.ChecksumIEEE(ihdr)
	b := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0d")
	b = append(b, ihdr...)
	return append(b, byte(c>>24), byte(c>>16), byte(c>>8), byte(c))
}
