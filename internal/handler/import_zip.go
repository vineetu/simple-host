package handler

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"

	"github.com/vsriram/simple-host/internal/tarball"
)

// Bound and count central-directory records before archive/zip allocates them.
// Require the directory to end at its end record so NewReader cannot scan an
// unbudgeted gap after a dishonest directory size/count.
func checkImportZIP(src io.ReaderAt, size int64, maxEntries int) (*io.SectionReader, error) {
	invalid := errors.New("invalid ZIP central directory")
	tail := make([]byte, min(size, 22+65535))
	if _, err := src.ReadAt(tail, size-int64(len(tail))); err != nil {
		return nil, invalid
	}
	end := -1
	for i := len(tail) - 22; i >= 0; i-- {
		if bytes.Equal(tail[i:i+4], []byte{'P', 'K', 5, 6}) {
			// Match archive/zip's last-signature selection, including a
			// signature embedded in a comment; never preflight another record.
			if i+22+int(binary.LittleEndian.Uint16(tail[i+20:])) != len(tail) {
				return nil, invalid
			}
			end = i
			break
		}
	}
	if end < 0 {
		return nil, invalid
	}
	e := tail[end:]
	if binary.LittleEndian.Uint32(e[4:8]) != 0 {
		return nil, invalid
	}
	count := uint64(binary.LittleEndian.Uint16(e[10:12]))
	diskCount := uint64(binary.LittleEndian.Uint16(e[8:10]))
	directorySize := uint64(binary.LittleEndian.Uint32(e[12:16]))
	offset := uint64(binary.LittleEndian.Uint32(e[16:20]))
	directoryEnd := size - int64(len(tail)) + int64(end)
	// A ZIP64 locator sits immediately before the ordinary end record.
	var locator [20]byte
	if directoryEnd >= 20 {
		if _, err := src.ReadAt(locator[:], directoryEnd-20); err != nil {
			return nil, invalid
		}
	}
	if binary.LittleEndian.Uint32(locator[:4]) == 0x07064b50 {
		if binary.LittleEndian.Uint32(locator[4:8]) != 0 || binary.LittleEndian.Uint32(locator[16:20]) != 1 {
			return nil, invalid
		}
		position := binary.LittleEndian.Uint64(locator[8:16])
		if position > uint64(directoryEnd-20) {
			return nil, invalid
		}
		var z [56]byte
		if _, err := src.ReadAt(z[:], int64(position)); err != nil {
			return nil, invalid
		}
		recordSize := binary.LittleEndian.Uint64(z[4:12])
		if binary.LittleEndian.Uint32(z[:4]) != 0x06064b50 || recordSize < 44 || recordSize > 4096 || position+12+recordSize != uint64(directoryEnd-20) || binary.LittleEndian.Uint64(z[16:24]) != 0 {
			return nil, invalid
		}
		zip64Count := binary.LittleEndian.Uint64(z[32:40])
		zip64Size := binary.LittleEndian.Uint64(z[40:48])
		zip64Offset := binary.LittleEndian.Uint64(z[48:56])
		if (diskCount != 0xffff && diskCount != zip64Count) ||
			(count != 0xffff && count != zip64Count) ||
			(directorySize != 0xffffffff && directorySize != zip64Size) ||
			(offset != 0xffffffff && offset != zip64Offset) {
			return nil, invalid
		}
		count = zip64Count
		if binary.LittleEndian.Uint64(z[24:32]) != count {
			return nil, invalid
		}
		directorySize = zip64Size
		offset = zip64Offset
		directoryEnd = int64(position)
	} else if diskCount != count || count == 0xffff || directorySize == 0xffffffff || offset == 0xffffffff {
		return nil, invalid
	}
	if count > uint64(max(0, maxEntries)) {
		return nil, errors.New("archive has too many entries")
	}
	if directorySize > 4<<20 || directorySize > uint64(max(0, maxEntries))*4096 {
		return nil, errors.New("ZIP central directory exceeds metadata budget")
	}
	if offset > uint64(directoryEnd) || directorySize != uint64(directoryEnd)-offset {
		return nil, invalid
	}
	directory := io.NewSectionReader(src, int64(offset), int64(directorySize))
	actual := 0
	for directory.Size() > 0 {
		pos, _ := directory.Seek(0, io.SeekCurrent)
		if pos == directory.Size() {
			break
		}
		var header [46]byte
		if _, err := io.ReadFull(directory, header[:]); err != nil || binary.LittleEndian.Uint32(header[:4]) != 0x02014b50 {
			return nil, invalid
		}
		actual++
		if actual > maxEntries {
			return nil, errors.New("archive has too many entries")
		}
		nameSize := int64(binary.LittleEndian.Uint16(header[28:30]))
		if nameSize > tarball.MaxPathLen {
			return nil, errors.New("archive path exceeds 1024 bytes")
		}
		variable := nameSize + int64(binary.LittleEndian.Uint16(header[30:32])) + int64(binary.LittleEndian.Uint16(header[32:34]))
		if variable > 4096 {
			return nil, errors.New("ZIP entry exceeds metadata budget")
		}
		next, _ := directory.Seek(variable, io.SeekCurrent)
		if next > directory.Size() {
			return nil, invalid
		}
	}
	if uint64(actual) != count {
		return nil, invalid
	}
	return io.NewSectionReader(src, 0, size), nil
}
