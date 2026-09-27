package handler

import (
	"regexp"
	"strings"
)

// Redaction of pasted error output for the setup assistant.
//
// The assistant takes pasted output (the installer, the person's AI agent,
// kubectl, docker or Caddy logs, the server's startup refusal) to diagnose.
// The page redacts it before the person sees what will be sent and clicks
// "Send for help"; the server applies the same rules again before anything
// reaches the model, in case a request did not come from that page. The rules
// and their order are the same as redact() in static/setup/assist.js, and
// testdata/setup-redact-cases.json holds the cases both are tested against.
//
// What goes: private key and certificate blocks, passwords in URLs (a "/" or
// "@" in the password included), Authorization, Cookie, Set-Cookie and
// X-...-Key/Token/Secret header values, bearer and basic credentials, a
// password given to curl -u / --user (also glued: -uuser:pass, -fsSu),
// --password and mysql -p, the value of any assignment whose name ends in
// KEY, SECRET, TOKEN, PASS, PASSWORD, PASSPHRASE, DSN, SALT, HASH, COOKIE,
// SIG or CREDENTIAL(S) (optionally followed by _ID, _BASE or _B64): at a line
// start, after a docker compose "service | " prefix, as a quoted JSON key
// anywhere, or as NAME=value anywhere, quoted values included. Then JWTs,
// Simple Host keys and tokens (shk_, shat_, shrt_, shac_, shc_, shcs_,
// shint_), common provider keys (UpCloud's ucat_ too), email addresses
// (non-ASCII ones included), and long hex or mixed-case base64 strings.
// Terminal colour codes and zero-width characters are removed first, so they
// cannot split a name from its value. Hostnames, IP addresses and paths stay:
// they are what a diagnosis needs. The rules catch the shapes they know; the
// page says so, and shows the person what will be sent before it is.

type setupRedactRule struct {
	re   *regexp.Regexp
	with string                  // the replacement, with ${n} groups
	keep func(match string) bool // when set, a match it keeps is left alone
}

const (
	setupRedactName     = `[A-Za-z0-9_.-]*(?:key|secret|token|pass|passwd|password|passphrase|pwd|dsn|salt|credentials?|cookie|sig|signature)(?:[_.-]?(?:base|b64|base64|id))?`
	setupRedactLineName = `[A-Za-z0-9_.-]*(?:key|secret|token|pass|passwd|password|passphrase|pwd|dsn|salt|hash|credentials?|cookie|sig|signature)(?:[_.-]?(?:base|b64|base64|id))?`
	setupRedactEmailCh  = `\x{a1}-\x{1fff}\x{2070}-\x{2fff}\x{3001}-\x{d7ff}\x{f900}-\x{ffef}`
)

var setupRedactRules = []setupRedactRule{
	{re: regexp.MustCompile(`(?s)-----BEGIN ([A-Z0-9 ]+)-----.*?(?:-----END [A-Z0-9 ]+-----|$)`), with: "[redacted ${1}]"},
	{re: regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)([^\t\n\f\r :/@]+):([^\t\n\f\r '"<>?#]+)@`), with: "${1}${2}:[redacted]@"},
	{re: regexp.MustCompile(`(?i)\b((?:proxy-)?authorization["']?[ \t]*[:=][ \t]*["']?)(?:([A-Za-z]+)([ \t]+))?[^\t\n\f\r '",]+`), with: "${1}${2}${3}[redacted]"},
	{re: regexp.MustCompile(`(?i)\b(set-cookie|cookie|x-[A-Za-z0-9-]*(?:key|token|secret|signature|auth|session)[A-Za-z0-9-]*)(["']?[ \t]*:[ \t]*["']?)[^\t\n\f\r '"][^\n'"]*`), with: "${1}${2}[redacted]"},
	{re: regexp.MustCompile(`(?i)\b(bearer|basic)([ \t]+)[A-Za-z0-9._~+/=-]{8,}`), with: "${1}${2}[redacted]"},
	{re: regexp.MustCompile(`(?m)(^|[ \t])(-[A-Za-z]*u|--user)([ \t]*|=)(?:(['"])([^\n:'"]+):[^\n'"]*|([^\t\n\f\r :'"]+):[^\t\n\f\r '"]+)`), with: "${1}${2}${3}${4}${5}${6}:[redacted]"},
	{re: regexp.MustCompile(`(^|[ \t])(--pass(?:word|wd)?)([ \t]+|=)(["']?)[^\t\n\f\r '"]+`), with: "${1}${2}${3}${4}[redacted]"},
	{re: regexp.MustCompile(`\b(mysql(?:dump|admin)?|mariadb(?:-dump|-admin)?)\b([^\n]*?[ \t])-p[^\t\n\f\r '"]+`), with: "${1}${2}-p[redacted]"},
	{re: regexp.MustCompile(`(?im)^([ \t]*(?:[A-Za-z0-9_.-]+[ \t]+\|[ \t]*)?(?:export[ \t]+)?["']?` + setupRedactLineName + `["']?[ \t]*[:=][ \t]*)[^\t\n\f\r ].*$`), with: "${1}[redacted]"},
	{re: regexp.MustCompile(`(?i)(["']` + setupRedactName + `["'][ \t]*:[ \t]*)(?:"(?:[^"\\\n]|\\.)*"|'[^'\n]*'|[^\t\n\f\r ,;}]+)`), with: "${1}[redacted]"},
	{re: regexp.MustCompile(`(?i)\b(` + setupRedactName + `)=(?:"[^"\n]*"|'[^'\n]*'|[^\t\n\f\r &;'",]+)`), with: "${1}=[redacted]"},
	{re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{2,}`), with: "[redacted token]"},
	{re: regexp.MustCompile(`\b(?:sh(?:k|at|rt|ac|c|cs|int)_|sk-|sk_|pk_|rk_|re_|ghp_|gho_|ghu_|ghs_|ghr_|github_pat_|glpat-|xai-|xox[abpors]-|ucat_)[A-Za-z0-9_-]{12,}`), with: "[redacted key]"},
	{re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), with: "[redacted key]"},
	{re: regexp.MustCompile(`[A-Za-z0-9._%+` + setupRedactEmailCh + `-]+[@\x{ff20}][A-Za-z0-9` + setupRedactEmailCh + `-]+(?:\.[A-Za-z0-9` + setupRedactEmailCh + `-]+)+`), with: "[email]"},
	{re: regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`), with: "[redacted]"},
	// Long base64-like strings with digits, lower and upper case: secrets,
	// not paths or names.
	{re: regexp.MustCompile(`[A-Za-z0-9+/_=-]{32,}`), with: "[redacted]", keep: func(m string) bool {
		return !strings.ContainsAny(m, "0123456789") || !strings.ContainsAny(m, "abcdefghijklmnopqrstuvwxyz") || !strings.ContainsAny(m, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	}},
}

// Terminal escape sequences: CSI (colours, cursor) and OSC (titles, links).
var setupRedactANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b\n]*(?:\x07|\x1b\\)?`)

// setupRedact returns s with line endings made \n, terminal escape
// sequences, zero-width characters and control characters other than tab
// and newline removed, and every secret-looking part replaced.
// Character classes are spelled out (not \s, \S) so the page's JavaScript,
// whose \s also takes Unicode spaces, matches exactly the same text.
func setupRedact(s string) string {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\u2028", "\n", "\u2029", "\n").Replace(s)
	s = setupRedactANSI.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		if r >= 0x200b && r <= 0x200d || r == 0x2060 || r == 0xfeff {
			return -1
		}
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f && !(r >= 0x80 && r < 0xa0)) {
			return r
		}
		return -1
	}, s)
	for _, rule := range setupRedactRules {
		if rule.keep == nil {
			s = rule.re.ReplaceAllString(s, rule.with)
			continue
		}
		keep, with := rule.keep, rule.with
		s = rule.re.ReplaceAllStringFunc(s, func(m string) string {
			if keep(m) {
				return m
			}
			return with
		})
	}
	return s
}
