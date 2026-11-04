package sites

import (
	"strings"
)

func PlatformNameLabel(host string, domains []string) (string, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, d := range domains {
		if !strings.HasSuffix(host, "."+d) {
			continue
		}
		if label := strings.TrimSuffix(host, "."+d); label != "" && !strings.Contains(label, ".") {
			return label, true
		}
	}
	return "", false
}
