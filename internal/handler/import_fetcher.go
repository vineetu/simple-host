package handler

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/config"
)

type importFetcher struct {
	client     *http.Client
	hosts      map[string]bool
	theme      string
	photos     bool
	max, total int64
	deadline   time.Time
	mu         sync.Mutex
}

func importSourceHosts(files []string) map[string]bool {
	hosts := map[string]bool{}
	for _, filename := range files {
		f, err := os.Open(filename)
		if err != nil {
			continue
		}
		dec := xml.NewDecoder(f)
		sawBase := false
		linkHost := ""
		for {
			t, err := dec.Token()
			if err != nil {
				break
			}
			if e, ok := t.(xml.StartElement); ok {
				if e.Name.Local == "item" {
					break
				}
				if e.Name.Local == "base_site_url" || e.Name.Local == "base_blog_url" {
					sawBase = true
					var raw string
					if dec.DecodeElement(&raw, &e) == nil {
						if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && u.Hostname() != "" {
							host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
							hosts[host] = true
							hosts["www."+host] = true
						}
					}
				}
				if e.Name.Local == "link" && linkHost == "" {
					var raw string
					if dec.DecodeElement(&raw, &e) == nil {
						if u, err := url.Parse(strings.TrimSpace(raw)); err == nil {
							linkHost = u.Hostname()
						}
					}
				}
			}
		}
		// Squarespace writes no base_site_url or base_blog_url. The channel
		// link is the site's own host, and its www twin.
		if !sawBase && linkHost != "" {
			host := strings.TrimPrefix(strings.ToLower(linkHost), "www.")
			hosts[host] = true
			hosts["www."+host] = true
		}
		f.Close()
	}
	return hosts
}
func newImportFetcher(hosts map[string]bool, theme string, photos bool, l config.Limits) *importFetcher {
	f := &importFetcher{hosts: hosts, theme: theme, photos: photos, max: int64(l.ImportFetchMaxMB) << 20, total: int64(l.ImportFetchTotalMB) << 20, deadline: time.Now().Add(15 * time.Minute)}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("host has no public address")
		}
		for _, ip := range ips {
			if !isPublicIP(ip.IP.String()) {
				return nil, errors.New("private network addresses are not allowed")
			}
		}
		var dial net.Dialer
		return dial.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second}
	f.client = &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return errors.New("too many redirects")
		}
		return f.allowed(req.URL)
	}}
	return f
}
func (f *importFetcher) allowed(u *url.URL) error {
	if (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return errors.New("only public HTTP and HTTPS addresses are allowed")
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil && !isPublicIP(host) {
		return errors.New("private network addresses are not allowed")
	}
	if !f.hosts[host] && !strings.HasSuffix(host, ".files.wordpress.com") && !strings.HasSuffix(host, ".wordpress.com") && !strings.HasSuffix(host, ".wp.com") && host != "fonts.googleapis.com" && host != "fonts.gstatic.com" && !squarespaceAssetHost(host) {
		return errors.New("host is not part of this WordPress site")
	}
	return nil
}
func squarespaceAssetHost(host string) bool {
	switch host {
	case "images.squarespace-cdn.com", "static1.squarespace.com", "static.squarespace.com", "assets.squarespace.com":
		return true
	}
	return false
}
func (f *importFetcher) Fetch(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if err = f.allowed(u); err != nil {
		return nil, err
	}
	if !f.photos && raw != f.theme && !strings.HasSuffix(strings.ToLower(u.Path), ".css") && u.Hostname() != "fonts.googleapis.com" {
		return nil, errors.New("copying photos is switched off")
	}
	ctx, cancel := context.WithDeadline(ctx, f.deadline)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "SimpleHostImporter/1.0 (+https://simple-host.app)")
	f.mu.Lock()
	remaining := f.total
	f.mu.Unlock()
	if remaining <= 0 {
		return nil, errors.New("import fetch budget used up")
	}
	res, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, errors.New("WordPress file was unavailable")
	}
	// Reserve bytes while reading, including failed and oversized responses. This
	// keeps concurrent converter fetches within the single import's byte budget.
	var body []byte
	buf := make([]byte, 32<<10)
	for {
		n, e := res.Body.Read(buf)
		if n > 0 {
			f.mu.Lock()
			f.total -= int64(n)
			over := f.total < 0
			f.mu.Unlock()
			if over || int64(len(body)+n) > f.max {
				return nil, errors.New("download is too large")
			}
			body = append(body, buf[:n]...)
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
	}
	if !f.photos && raw != f.theme && !strings.Contains(strings.ToLower(res.Header.Get("Content-Type")), "text/css") {
		return nil, errors.New("only the theme page and CSS may be copied")
	}
	return body, nil
}
