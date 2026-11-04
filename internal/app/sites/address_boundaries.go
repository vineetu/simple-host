package sites

import (
	"errors"
	"path"
	"regexp"
	"strings"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/model"
	rules "github.com/vsriram/simple-host/internal/rules/sites"
)

var visitorSitenameRe = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)
var familyLabelRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func HandleSeed(user *model.User, siteDomain string) string {
	if !user.IsAdmin || user.Username != "admin" {
		return user.Username
	}
	label, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(siteDomain)), ".")
	if base := SanitizeHandleBase(label); label != "" && base == label && !rules.LabelReservedForNew(base) {
		return base
	}
	return "organiser"
}

func HandleAddressable(handle string) bool {
	return rules.VisitorHandle.MatchString(handle) && rules.HandleIsLabel(handle) && !rules.LabelReserved(handle)
}

func SplitContentHostPath(p string) (handle, sitename string, ok bool) {
	cleaned := path.Clean("/" + p)
	parts := strings.Split(strings.Trim(cleaned, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	handle, sitename = parts[0], parts[1]
	if !rules.VisitorHandle.MatchString(handle) || !visitorSitenameRe.MatchString(sitename) {
		return "", "", false
	}
	prefix := "/" + handle + "/" + sitename
	if cleaned != prefix && !strings.HasPrefix(cleaned, prefix+"/") {
		return "", "", false
	}
	return handle, sitename, true
}

func FamilyLabelOK(f model.AddressFamily, label string, l edition.Limits) bool {
	if !familyLabelRE.MatchString(label) || strings.HasPrefix(label, "xn--") {
		return false
	}
	for _, r := range l.ReservedFamilyLabels() {
		if label == r {
			return false
		}
	}
	return rules.ValidSiteName.MatchString(f.SitePrefix + label)
}

var claimLabelRE = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)
var ErrSubdomainReserved = errors.New("that name is reserved; pick another")

// ClaimPlatformAddress checks a parsed platform label and constructs its new address.
func ClaimPlatformAddress(label, base, siteDomain, contentHost, cnameTarget string) (string, error) {
	if !claimLabelRE.MatchString(label) || label[0] == '-' || label[len(label)-1] == '-' {
		return "", errors.New("the name may use lowercase letters, numbers and hyphens (not at the start or end), up to 63 characters")
	}
	if strings.HasPrefix(label, "xn--") {
		return "", errors.New("internationalised names are not supported")
	}
	full := label + "." + base
	if rules.LabelReservedForNew(label) {
		return "", ErrSubdomainReserved
	}
	for _, b := range []string{base, strings.ToLower(siteDomain)} {
		if strings.EqualFold(label+"."+b, contentHost) || strings.EqualFold(label+"."+b, cnameTarget) {
			return "", ErrSubdomainReserved
		}
	}
	return full, nil
}

// TwinHost maps a user address between platform bases, excluding platform hosts.
func TwinHost(host, from, to string) string {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if !strings.HasSuffix(host, "."+from) {
		return ""
	}
	labels := strings.TrimSuffix(host, "."+from)
	parts := strings.Split(labels, ".")
	switch {
	case labels == "" || len(parts) > 2:
		return ""
	case len(parts) == 1 && (parts[0] == "www" || rules.LabelReserved(parts[0])):
		return ""
	case len(parts) == 2 && (parts[0] == "" || rules.LabelReserved(parts[1])):
		return ""
	}
	return labels + "." + to
}

// PlatformHostTarget selects the app host for an apex or reserved platform host.
// Inputs are hostnames already decoded at the transport boundary.
func PlatformHostTarget(host, base, target string) string {
	if host == base || host == "www."+base {
		return target
	}
	if !strings.HasSuffix(host, "."+base) {
		return ""
	}
	label := strings.TrimSuffix(host, "."+base)
	if label == "" || strings.Contains(label, ".") || !rules.LabelReserved(label) {
		return ""
	}
	return target
}

// LegacyHostLabel selects the single-label legacy address routed by the host.
// Inputs are hostnames already decoded at the transport boundary.
func LegacyHostLabel(host, base string) (string, bool) {
	if !strings.HasSuffix(host, "."+base) {
		return "", false
	}
	label := strings.TrimSuffix(host, "."+base)
	if label == "" || strings.Contains(label, ".") || rules.LabelReserved(label) {
		return "", false
	}
	return label, true
}

// LegacyBaseLabels selects a user's one- or two-label address for a base move.
func LegacyBaseLabels(host, base string) ([]string, bool) {
	labels := strings.TrimSuffix(host, "."+base)
	parts := strings.Split(labels, ".")
	switch len(parts) {
	case 1:
		if parts[0] == "" || parts[0] == "www" || rules.LabelReserved(parts[0]) {
			return nil, false
		}
	case 2:
		if parts[0] == "" || !HandleAddressable(parts[1]) {
			return nil, false
		}
	default:
		return nil, false
	}
	return parts, true
}

// ClaimedReturnHost excludes platform infrastructure before OAuth's binding lookup.
func ClaimedReturnHost(host string, bases []string, contentHost, cnameTarget, publicHost string) bool {
	label, ok := "", false
	for _, b := range bases {
		normalized := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
		base := strings.ToLower(strings.TrimSpace(b))
		if base != "" && strings.HasSuffix(normalized, "."+base) {
			candidate := strings.TrimSuffix(normalized, "."+base)
			if candidate != "" && !strings.Contains(candidate, ".") {
				label, ok = candidate, true
				break
			}
		}
	}
	if !ok || label == "www" || rules.ReservedSubdomainSet[label] {
		return false
	}
	host = strings.ToLower(host)
	return !strings.EqualFold(host, contentHost) && !strings.EqualFold(host, strings.TrimSuffix(cnameTarget, ".")) && !strings.EqualFold(host, publicHost)
}
