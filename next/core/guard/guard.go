// Package guard is the outbound address guard for addresses a browser hands
// us (push endpoints): only public HTTPS hosts, checked again at dial time so
// a second DNS answer cannot point a send inside the network. PublicIP and
// the dialer are copied from the old host/data/notify/client.go.
package guard

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// ErrRefused is returned for an address that is not public.
var ErrRefused = errors.New("refused_address")

// PublicIP: a globally routable unicast address (not loopback, private,
// link-local, carrier-grade NAT, unspecified or multicast).
func PublicIP(s string) bool {
	ip, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fec0::/10"),
}

// Client is an HTTP client that dials public addresses only, over TLS 1.2+,
// and never follows a redirect. loopback also lets it reach this machine:
// only the test kit sets it, so the fakes (with their own certificates) can
// stand in for the internet.
func Client(timeout time.Duration, loopback bool) *http.Client {
	public := func(ip string) bool {
		if PublicIP(ip) {
			return true
		}
		p := net.ParseIP(ip)
		return loopback && p != nil && p.IsLoopback()
	}
	dialer := net.Dialer{Timeout: 2 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		ip, _, err := net.SplitHostPort(address)
		if err != nil || !public(ip) {
			return ErrRefused
		}
		return nil
	}}
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrRefused
		}
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(addrs) == 0 {
			return nil, errors.New("dns_error")
		}
		for _, a := range addrs {
			if a.Zone != "" || !public(a.IP.String()) {
				return nil, ErrRefused
			}
		}
		for _, a := range addrs {
			conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
			if e != nil {
				err = e
				continue
			}
			remote, _, e := net.SplitHostPort(conn.RemoteAddr().String())
			if e != nil || !public(remote) {
				conn.Close()
				return nil, ErrRefused
			}
			return conn, nil
		}
		return nil, err
	}
	transport := &http.Transport{Proxy: nil, DialContext: dial, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: loopback}}
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
