package photos

import "encoding/binary"

// jpegFile is what the segments read so far declare about the image.
type jpegFile struct {
	scans, iccBytes int
	frameSeen       bool
}

// checkJPEG validates the marker stream through EOI, including entropy byte
// stuffing and scan boundaries. DecodeConfig then validates the image header.
// No pixel decode.
func checkJPEG(b []byte) error {
	var f jpegFile
	for off := 2; off < len(b); {
		marker, next, err := jpegMarkerAt(b, off)
		if err != nil {
			return err
		}
		if marker == 0xd9 {
			if next == len(b) && f.scans > 0 {
				return nil
			}
			return ErrContainer
		}
		payload, err := jpegPayloadAt(b, next)
		if err != nil {
			return err
		}
		if err := f.add(marker, payload); err != nil {
			return err
		}
		off = next + 2 + len(payload)
		if marker == 0xda {
			f.scans++
			if off, err = jpegEntropyEnd(b, off); err != nil {
				return err
			}
		}
	}
	return ErrContainer
}

func jpegMarkerAt(b []byte, off int) (byte, int, error) {
	if b[off] != 255 {
		return 0, 0, ErrContainer
	}
	for off < len(b) && b[off] == 255 {
		off++
	}
	if off >= len(b) {
		return 0, 0, ErrContainer
	}
	return b[off], off + 1, nil
}

func jpegPayloadAt(b []byte, off int) ([]byte, error) {
	if off+2 > len(b) {
		return nil, ErrContainer
	}
	n := int(binary.BigEndian.Uint16(b[off : off+2]))
	if n < 2 || n > len(b)-off {
		return nil, ErrContainer
	}
	return b[off+2 : off+n], nil
}

// jpegEntropyEnd returns where the non-empty entropy-coded data starting at
// off ends: at the first marker that is not a stuffed zero or a restart.
func jpegEntropyEnd(b []byte, off int) (int, error) {
	start := off
	for off < len(b) {
		if b[off] != 255 {
			off++
			continue
		}
		if off+1 >= len(b) {
			return 0, ErrContainer
		}
		if b[off+1] == 0 || b[off+1] >= 0xd0 && b[off+1] <= 0xd7 {
			off += 2
			continue
		}
		break
	}
	if off == start {
		return 0, ErrContainer
	}
	return off, nil
}

func (f *jpegFile) add(marker byte, payload []byte) error {
	switch marker {
	case 0xe1:
		return ErrMetadata // All APP1, including Exif and both XMP forms.
	case 0xe0, 0xe2, 0xee:
		return f.addApp(marker, payload)
	case 0xc0, 0xc1, 0xc2:
		if f.frameSeen || f.scans != 0 {
			return ErrContainer
		}
		f.frameSeen = true
	case 0xda:
		if !f.frameSeen || len(payload) < 6 || payload[0] < 1 || payload[0] > 4 || len(payload) != 4+2*int(payload[0]) {
			return ErrContainer
		}
	case 0xc4, 0xdb, 0xdd:
	default:
		return ErrContainer // No comments or arbitrary application data.
	}
	return nil
}

func (f *jpegFile) addApp(marker byte, payload []byte) error {
	switch marker {
	case 0xe0:
		if len(payload) < 14 || string(payload[:5]) != "JFIF\x00" || len(payload) != 14+3*int(payload[12])*int(payload[13]) {
			return ErrContainer
		}
	case 0xe2:
		if len(payload) < 14 || string(payload[:12]) != "ICC_PROFILE\x00" {
			return ErrContainer
		}
		f.iccBytes += len(payload)
		if f.iccBytes > 16*1024 {
			return ErrMetadata
		}
	case 0xee:
		if len(payload) != 12 || string(payload[:5]) != "Adobe" {
			return ErrMetadata
		}
	}
	return nil
}
