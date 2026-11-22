package sites

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseIfMatch reads the optional If-Match header of a deploy, update or
// rollback: the active version the caller last read, "3" or 3. Agents send it
// on sites other people also publish so two members never overwrite each
// other unknowingly. 0 when absent or *, and always 0 where there are no
// teams (enabled: the Simple Host program's personal sites); a malformed value
// is refused (invalid_if_match).
func ParseIfMatch(raw string, enabled bool) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "*" || !enabled {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(raw, "W/"), `"`))
	if err != nil || n < 1 {
		return 0, &Error{Code: "invalid_if_match", Message: `If-Match must be the site's active version in quotes, e.g. If-Match: "3"`}
	}
	return n, nil
}

// VersionChanged refuses a change whose If-Match names a version that is no
// longer the active one: someone else published or rolled back meanwhile.
func VersionChanged(active int) error {
	return &Error{Code: "version_changed", Message: fmt.Sprintf("the site changed since you read it: its active version is now %d; read it again and redo your change on that version", active)}
}
