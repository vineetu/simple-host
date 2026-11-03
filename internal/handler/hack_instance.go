package handler

import (
	"net/url"
	"strings"
)

// Full Hack instances serve the same UI and skills, with their own connector
// and event addresses. Simple Host's hosted-product and control-plane links
// keep their existing meaning.
func (p *assetPipeline) hackInstanceText(data []byte) []byte {
	if !p.pres.HackAssets || p.inkBaseURL() == canonicalHackBaseURL {
		return data
	}
	u, err := url.Parse(p.inkBaseURL())
	if err != nil || u.Host == "" {
		return data
	}
	return []byte(strings.NewReplacer("https://simple-hack.app", strings.TrimRight(p.inkBaseURL(), "/"), "simple-hack.app", u.Host).Replace(string(data)))
}
