package connector

import (
	"log"
	"net"
	"net/url"
	"strings"
)

// ClientMetadata is a dynamic client registration request (RFC 7591).
type ClientMetadata struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

// CheckClientMetadata accepts a registration: 1 to max valid redirect URIs,
// a supported token endpoint auth method (client_secret_basic when unset,
// RFC 7591 §2) which it returns, the code grant and refresh only.
func CheckClientMetadata(in ClientMetadata, max int) (string, *ConnectorError) {
	if len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > max {
		return "", &ConnectorError{Code: "invalid_redirect_uri", Description: "between 1 and 10 redirect_uris are required"}
	}
	for _, u := range in.RedirectURIs {
		if !ValidRedirectURI(u) {
			log.Printf("connector: register refused redirect_uri=%q", truncateForLog(u, 300))
			return "", &ConnectorError{Code: "invalid_redirect_uri", Description: "redirect URIs must be https, http on a loopback address, or an app's own scheme, with no fragment"}
		}
	}
	method := in.TokenEndpointAuthMethod
	if method == "" {
		method = "client_secret_basic"
	}
	if method != "none" && method != "client_secret_post" && method != "client_secret_basic" {
		return "", &ConnectorError{Code: "invalid_client_metadata", Description: "token_endpoint_auth_method must be none, client_secret_post or client_secret_basic"}
	}
	for _, g := range in.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return "", &ConnectorError{Code: "invalid_client_metadata", Description: "only the authorization_code and refresh_token grants are supported"}
		}
	}
	for _, t := range in.ResponseTypes {
		if t != "code" {
			return "", &ConnectorError{Code: "invalid_client_metadata", Description: "only the code response type is supported"}
		}
	}
	return method, nil
}

// ValidRedirectURI is the registration rule (RFC 8252, MCP authorization):
// https anywhere; http only on a loopback address, for an app on the
// person's own machine; or an app's own URL scheme (cursor://, vscode://,
// com.example.app:/), which only that installed app answers. No fragment, no
// credentials in the URL. Every client is held to PKCE, which is what keeps
// a code sent to a shared or claimed scheme useless to anyone else.
func ValidRedirectURI(raw string) bool {
	if raw == "" || len(raw) > 2000 || strings.ContainsAny(raw, " \t\r\n#\\") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.User != nil || u.Fragment != "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return u.Host != ""
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return true
		}
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	}
	return validAppScheme(u)
}

// blockedRedirectSchemes are schemes a browser or the operating system
// handles itself (scripts, local files, other network protocols, settings
// and troubleshooting tools), so they are never an app's sign-in return.
var blockedRedirectSchemes = map[string]bool{
	"javascript": true, "vbscript": true, "data": true, "blob": true, "file": true,
	"filesystem": true, "about": true, "view-source": true, "resource": true,
	"chrome": true, "edge": true, "opera": true, "brave": true, "intent": true,
	"jar": true, "ftp": true, "ftps": true, "sftp": true, "ws": true, "wss": true,
	"ssh": true, "telnet": true, "smb": true, "nfs": true, "afp": true, "ldap": true,
	"mailto": true, "tel": true, "sms": true, "callto": true, "facetime": true,
	"search": true, "search-ms": true, "shell": true, "hcp": true, "help": true,
	"mk": true, "its": true, "res": true, "urn": true, "smsto": true, "mms": true,
	"facetime-audio": true, "skype": true, "sip": true, "steam": true, "market": true,
	"chrome-extension": true, "moz-extension": true, "safari-extension": true,
	"webcal": true, "feed": true, "itunes": true, "content": true, "irc": true, "magnet": true, "git": true, "svn": true,
}

// blockedRedirectSchemePrefixes cover families of system handlers: Windows
// ms-* tools, Apple's installers and settings (itms-services installs an app
// from a URL), and the operating system's own x-apple handlers.
var blockedRedirectSchemePrefixes = []string{"ms-", "itms", "x-apple", "apple", "microsoft-", "windows", "android", "google-"}

// validAppScheme accepts a private-use scheme (RFC 8252 section 7.1):
// cursor://anysphere.cursor-mcp/oauth/callback, or the reverse-domain
// com.example.app:/oauth2redirect. The address carries no query, so a
// registration cannot smuggle parameters to whatever handles the scheme
// (authorize errors go back before anyone signs in), and a hostless one must
// use a reverse-domain scheme, as RFC 8252 asks.
func validAppScheme(u *url.URL) bool {
	scheme := u.Scheme
	if len(scheme) < 2 || len(scheme) > 64 || blockedRedirectSchemes[scheme] || u.RawQuery != "" || u.ForceQuery {
		return false
	}
	for _, p := range blockedRedirectSchemePrefixes {
		if strings.HasPrefix(scheme, p) {
			return false
		}
	}
	if u.Host != "" {
		return u.Port() == "" && !strings.ContainsAny(u.Host, "[]%")
	}
	return strings.Contains(scheme, ".") && strings.HasPrefix(u.Path, "/")
}

// truncateForLog is s cut to n bytes (valid UTF-8) for a log line.
func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}
