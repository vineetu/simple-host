package model

import (
	"strconv"
	"unicode/utf8"
)

// LegacyJSONStorable reports whether JSON text body has none of what jsonb refuses.
// Backslashes appear only inside strings in JSON, so escapes are found by
// scanning the bytes.
func LegacyJSONStorable(body []byte) bool {
	if !utf8.Valid(body) {
		return false
	}
	hex4 := func(i int) (rune, bool) {
		if i+6 > len(body) || body[i] != '\\' || body[i+1] != 'u' {
			return 0, false
		}
		n, err := strconv.ParseUint(string(body[i+2:i+6]), 16, 32)
		return rune(n), err == nil
	}
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			continue
		}
		if i+1 < len(body) && body[i+1] == 'u' {
			r, ok := hex4(i)
			switch {
			case !ok:
			case r == 0:
				return false
			case r >= 0xD800 && r <= 0xDBFF:
				lo, ok := hex4(i + 6)
				if !ok || lo < 0xDC00 || lo > 0xDFFF {
					return false
				}
				i += 6
			case r >= 0xDC00 && r <= 0xDFFF:
				return false
			}
		}
		i++ // the escaped character: \\ never starts another escape
	}
	return true
}
