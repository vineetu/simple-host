package access

import (
	"strings"
	"testing"
)

func TestVisitorRedactsEmbeddedAccountIDs(t *testing.T) {
	id := "01234567-89ab-cdef-0123-456789abcdef"
	for _, text := range []string{"author_" + id, id + "_suffix", "x" + strings.ToUpper(id) + "y"} {
		if got := VisitorPrivateData(text).(string); strings.Contains(strings.ToLower(got), id) {
			t.Fatalf("identity survived: %s", got)
		}
	}
}
