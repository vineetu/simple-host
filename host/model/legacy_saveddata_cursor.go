package model

import (
	"strconv"
	"strings"
	"time"
)

// LegacyDeletedCursor parses the cursor used for recently deleted items.
func ParseLegacyDeletedCursor(v string) LegacyDeletedCursor {
	at, id, ok := strings.Cut(v, "_")
	if !ok {
		return LegacyDeletedCursor{}
	}
	us, err1 := strconv.ParseInt(at, 10, 64)
	n, err2 := strconv.ParseInt(id, 10, 64)
	if err1 != nil || err2 != nil || n <= 0 {
		return LegacyDeletedCursor{}
	}
	return LegacyDeletedCursor{At: time.UnixMicro(us), ID: n}
}

// LegacyDeletedNext formats the cursor for a recently deleted item.
func LegacyDeletedNext(it LegacyDeletedItem) string {
	return strconv.FormatInt(it.DeletedAt.UnixMicro(), 10) + "_" + strconv.FormatInt(it.ID, 10)
}
