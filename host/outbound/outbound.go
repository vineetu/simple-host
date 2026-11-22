// Package outbound is the one guard for connections this program makes to
// addresses someone else chose (webhooks, web push, a site secret's
// destination): every name is resolved here, every address must be public,
// and each dialled address is checked again at connect time, so a second DNS
// answer (rebinding) cannot reach inside the network.
package outbound

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"syscall"
	"time"
)

// Failure is a refusal or connection outcome category ("refused_address",
// "dns_error", "timeout").
type Failure string

func (e Failure) Error() string { return string(e) }

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

// Dialer connects only to public addresses.
type Dialer struct {
	// Resolver replaces DNS: tests answer for names they make up, and a
	// webhook challenge dials exactly the addresses it charged.
	Resolver func(context.Context, string) ([]net.IPAddr, error)
	// AllowLoopback admits loopback addresses (tests' local servers).
	AllowLoopback bool
	// Refuse are more addresses never dialled, such as this server's own.
	Refuse map[string]bool
}

// Public reports whether d may connect to ip.
func (d Dialer) Public(ip string) bool {
	if d.Refuse[ip] {
		return false
	}
	if PublicIP(ip) {
		return true
	}
	parsed := net.ParseIP(ip)
	return d.AllowLoopback && parsed != nil && parsed.IsLoopback()
}

// lookup resolves host (a literal address resolves to itself).
func (d Dialer) lookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IPAddr{{IP: ip}}, nil
	}
	resolve := d.Resolver
	if resolve == nil {
		resolve = net.DefaultResolver.LookupIPAddr
	}
	addrs, err := resolve(ctx, host)
	var ne net.Error
	switch {
	case errors.As(err, &ne) && ne.Timeout():
		return nil, Failure("timeout")
	case err != nil:
		return nil, Failure("dns_error")
	}
	return addrs, nil
}

// Addresses are host's addresses when every one of them is public.
func (d Dialer) Addresses(ctx context.Context, host string) ([]net.IPAddr, error) {
	addrs, err := d.lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, Failure("dns_error")
	}
	for _, a := range addrs {
		if a.Zone != "" || !d.Public(a.IP.String()) {
			return nil, Failure("refused_address")
		}
	}
	return addrs, nil
}

// Dial connects to the first reachable public address of address's host,
// refusing any connection whose remote end is not public: the address is
// checked before the connect (Control) and after it (RemoteAddr).
func (d Dialer) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, Failure("refused_address")
	}
	addrs, err := d.Addresses(ctx, host)
	if err != nil {
		return nil, err
	}
	nd := net.Dialer{Timeout: 2 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		ip, _, err := net.SplitHostPort(address)
		if err != nil || !d.Public(ip) {
			return Failure("refused_address")
		}
		return nil
	}}
	for _, a := range addrs {
		conn, e := nd.DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
		if e != nil {
			err = e
			continue
		}
		remote, _, e := net.SplitHostPort(conn.RemoteAddr().String())
		if e != nil || !d.Public(remote) {
			conn.Close()
			return nil, Failure("refused_address")
		}
		return conn, nil
	}
	return nil, err
}
