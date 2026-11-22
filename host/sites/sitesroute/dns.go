package sitesroute

import "github.com/vsriram/simple-host/host/sites"

type DNSRecord struct {
	Type  string `json:"type"`
	Host  string `json:"host"`
	Value string `json:"value"`
}

// dnsRecordFor returns the DNS record a user must add to point their domain at us.
// An apex/registrable domain (e.g. agent-deploy.dev) can't use a CNAME, so it gets
// an A record to the box IP; a subdomain (e.g. recipes.brand.com) gets a CNAME.
func (h *Handler) DNSRecordFor(domain string) DNSRecord {
	if h.CustomDomainIP != "" && sites.IsApexDomain(domain) {
		return DNSRecord{Type: "A", Host: domain, Value: h.CustomDomainIP}
	}
	return DNSRecord{
		Type:  "CNAME",
		Host:  domain,
		Value: h.CNAMETarget,
	}
}

// ProofRecordFor is the DNS TXT record that proves the domain is the
// owner's: _simple-host.<domain> holding the site's token. nil without a token.
func ProofRecordFor(domain, token string) *DNSRecord {
	if token == "" {
		return nil
	}
	return &DNSRecord{Type: "TXT", Host: DomainProofHost(domain), Value: token}
}

func DomainProofHost(domain string) string { return "_simple-host." + domain }
