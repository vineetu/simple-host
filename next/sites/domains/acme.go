package domains

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"

	"simplehost/app"
)

// challenges are the HTTP challenge answers of orders in progress, token ->
// key authorization. Only this process asks for certificates.
var challenges sync.Map

// answerChallenge is GET /.well-known/acme-challenge/{token}.
func answerChallenge(w http.ResponseWriter, r *http.Request) {
	v, ok := challenges.Load(r.PathValue("token"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(v.(string)))
}

func certDir(a *app.App) string { return filepath.Join(a.Config.DataDir, "certs") }

// certificate is when name's certificate on disk ends (false: none).
func certificate(a *app.App, name string) (time.Time, bool) {
	b, err := os.ReadFile(filepath.Join(certDir(a), name, "fullchain.pem"))
	if err != nil {
		return time.Time{}, false
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return time.Time{}, false
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, false
	}
	return c.NotAfter, true
}

// issue gets a certificate for name from the first issuer that gives one,
// in order: Let's Encrypt, Google, ZeroSSL.
func issue(ctx context.Context, a *app.App, name string) (time.Time, error) {
	var errs []error
	for i, dir := range a.Config.ACMEDirectories {
		binding := ""
		if i < len(a.Config.ACMEBindings) {
			binding = a.Config.ACMEBindings[i]
		}
		exp, err := issueFrom(ctx, a, dir, binding, name)
		if err == nil {
			return exp, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", dir, err))
	}
	return time.Time{}, errors.Join(errs...)
}

// issueFrom runs one ACME order for name with the HTTP challenge, and writes
// the certificate and its key under certs/<name>/.
func issueFrom(ctx context.Context, a *app.App, dir, binding, name string) (time.Time, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	key, err := accountKey(a, dir)
	if err != nil {
		return time.Time{}, err
	}
	c := &acme.Client{Key: key, DirectoryURL: dir, HTTPClient: a.HTTP, UserAgent: "simple-host"}
	acct := &acme.Account{}
	if kid, mac, ok := strings.Cut(binding, ":"); ok {
		k, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(mac, "="))
		if err != nil {
			return time.Time{}, errors.New("the account binding key is not base64url")
		}
		acct.ExternalAccountBinding = &acme.ExternalAccountBinding{KID: kid, Key: k}
	}
	if _, err := c.Register(ctx, acct, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return time.Time{}, err
	}
	order, err := c.AuthorizeOrder(ctx, acme.DomainIDs(name))
	if err != nil {
		return time.Time{}, err
	}
	for _, u := range order.AuthzURLs {
		z, err := c.GetAuthorization(ctx, u)
		if err != nil {
			return time.Time{}, err
		}
		if z.Status == acme.StatusValid {
			continue
		}
		var ch *acme.Challenge
		for _, x := range z.Challenges {
			if x.Type == "http-01" {
				ch = x
			}
		}
		if ch == nil {
			return time.Time{}, errors.New("the issuer offered no HTTP challenge")
		}
		answer, err := c.HTTP01ChallengeResponse(ch.Token)
		if err != nil {
			return time.Time{}, err
		}
		challenges.Store(ch.Token, answer)
		defer challenges.Delete(ch.Token)
		if _, err := c.Accept(ctx, ch); err != nil {
			return time.Time{}, err
		}
		if _, err := c.WaitAuthorization(ctx, z.URI); err != nil {
			return time.Time{}, err
		}
	}
	if order, err = c.WaitOrder(ctx, order.URI); err != nil {
		return time.Time{}, err
	}
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return time.Time{}, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{name}}, certKey)
	if err != nil {
		return time.Time{}, err
	}
	chain, _, err := c.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return time.Time{}, err
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return time.Time{}, err
	}
	var full []byte
	for _, der := range chain {
		full = append(full, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	keyDER, err := x509.MarshalECPrivateKey(certKey)
	if err != nil {
		return time.Time{}, err
	}
	out := filepath.Join(certDir(a), name)
	if err := write(out, "privkey.pem", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})); err != nil {
		return time.Time{}, err
	}
	return leaf.NotAfter, write(out, "fullchain.pem", full)
}

// accountKey is this service's account key with one issuer, made on first use.
func accountKey(a *app.App, dir string) (*ecdsa.PrivateKey, error) {
	sum := sha256.Sum256([]byte(dir))
	folder := filepath.Join(certDir(a), "_accounts")
	path := filepath.Join(folder, hex.EncodeToString(sum[:8])+".pem")
	if b, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(b)
		if block == nil {
			return nil, errors.New("the account key " + path + " is damaged")
		}
		return x509.ParseECPrivateKey(block.Bytes)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return key, write(folder, filepath.Base(path), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
}

// write replaces dir/name whole (a temporary file, then a rename), readable
// only by this service and the web server's group.
func write(dir, name string, b []byte) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+name+".tmp")
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}
