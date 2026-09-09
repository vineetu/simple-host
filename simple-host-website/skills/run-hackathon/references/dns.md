# The two DNS records

Both are `A` records pointing at the server's **public IPv4** address.

| Name | Type | Value |
|---|---|---|
| `<event>` | A | the server's IPv4 |
| `sites.<event>` | A | the same IPv4 |

For an event called `builds` on `example.com`, that is `builds.example.com` and
`sites.builds.example.com`.

Use a short TTL, 60 to 300 seconds, so teardown takes effect quickly.

## Wait for them before installing

```bash
dig +short builds.example.com A
dig +short sites.builds.example.com A
```

Both must return the server's address. Installing early works, but certificate
issuance fails and the organiser sees browser warnings, which is far more
alarming than waiting.

## Per registrar

The organiser adds these themselves; you cannot, and should not ask for their
registrar password.

- **Vercel.** Domains, pick the domain, add a record. Name is the subdomain
  only, not the full hostname.
- **Cloudflare.** DNS, add record. **Turn the proxy off, grey cloud not orange.**
  Proxied records break certificate issuance for this setup and hide the real IP
  from Let's Encrypt.
- **GoDaddy.** DNS, Manage Zones, add. Name is the subdomain only.
- **Porkbun.** Details, DNS records. Type A, host is the subdomain only.
- **Namecheap.** Advanced DNS, Add New Record, A Record.

The same guidance in more depth lives in the `connect-domain` skill's
`references/registrars.md`.
