package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/vsriram/simple-host/internal/webassets"
)

// Site-side asset/text code for the Hack edition. Stays in internal/handler
// (cleanup 3.2 slice 2a): every function and method here is either a method
// on *assetPipeline (assets.go's type, which must stay in this package
// until it moves to the proposed internal/webassets per
// docs/architecture/target.md §2) or a free function staying files
// (authmd.go, chrome.go, discovery.go, instancehost.go, skillshub.go,
// ui.go) call directly. Moving Hack code (internal/hackhttp, once the real
// `git mv` happens) reaches the exported methods through the hackPages
// interface (hack.go); nothing here is duplicated, only relocated from
// hack_instance.go, hack_get_started.go, hack_connect_steps.go and
// hack_docs.go, which either move verbatim or shrink to their
// registrar/business-logic remainder.

// HackInstanceText rewrites simple-hack.app's own address to this Hack
// instance's, in served Hack text. Full Hack instances serve the same UI
// and skills, with their own connector and event addresses. Simple Host's
// hosted-product and control-plane links keep their existing meaning.
//
// Moved verbatim from hack_instance.go (cleanup 3.2 slice 2a) and exported:
// a method with an *assetPipeline receiver must live in this package, and
// moving code (hack_get_started.go) reaches it through hackPages.
func (p *assetPipeline) HackInstanceText(data []byte) []byte {
	if !p.pres.HackAssets || p.inkBaseURL() == canonicalHackBaseURL {
		return data
	}
	u, err := url.Parse(p.inkBaseURL())
	if err != nil || u.Host == "" {
		return data
	}
	return []byte(strings.NewReplacer("https://simple-hack.app", strings.TrimRight(p.inkBaseURL(), "/"), "simple-hack.app", u.Host).Replace(string(data)))
}

// HackSkillNames are the five Simple Hack skills bundled in the hosted zip.
// Moved verbatim from hack_get_started.go: skillshub.go's hostedHackSkill
// (staying) needs it, and BuildHackSkillsZip below (a *assetPipeline
// method) must live in this package regardless.
var HackSkillNames = []string{"run-hackathon", "join-hackathon", "judge-hackathon", "website-deploy", "website-deploy-builder"}

// hostedHackSkill reports whether name is one of the five bundled Hack
// skills. Moved verbatim from hack_get_started.go: skillshub.go calls it.
func hostedHackSkill(name string) bool {
	for _, skill := range HackSkillNames {
		if name == skill {
			return true
		}
	}
	return false
}

// BuildHackSkillsZip is the hosted bundle's five Simple Hack skills from the
// canonical embed, built once and cached. Moved verbatim from
// hack_get_started.go (a *assetPipeline method must live in this package).
func (p *assetPipeline) BuildHackSkillsZip() ([]byte, error) {
	z := &p.hackSkillsZip
	z.once.Do(func() {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, name := range HackSkillNames {
			root, err := fs.Sub(webassets.HackSkills, "skills/"+name)
			if err != nil {
				z.err = err
				break
			}
			err = fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil || path == "." || entry.IsDir() {
					return walkErr
				}
				data, err := fs.ReadFile(root, path)
				if err != nil {
					return err
				}
				dst, err := zw.Create(name + "/" + path)
				if err != nil {
					return err
				}
				_, err = dst.Write(p.skillServedText(name+"/"+path, data))
				return err
			})
			if err != nil {
				z.err = err
				break
			}
		}
		if err := zw.Close(); z.err == nil {
			z.err = err
		}
		if z.err == nil {
			z.bytes = buf.Bytes()
		}
	})
	return z.bytes, z.err
}

// ServeHackSkillsZip serves BuildHackSkillsZip at /hack-skills.zip.
//
// Exported, a method rather than a free function taking *assetPipeline
// (cleanup 3.2 slice 2a): moved verbatim from hack_get_started.go (ui.go,
// staying, also calls this; RegisterHackGetStarted, moving, reaches it
// through hackPages).
func (p *assetPipeline) ServeHackSkillsZip() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := p.BuildHackSkillsZip()
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="simple-hack-skills.zip"`)
		http.ServeContent(w, r, "simple-hack-skills.zip", skillsModTime, bytes.NewReader(data))
	}
}

// RewriteHackPluginZip keeps binary assets intact and rewrites only our
// text (.md/.yaml/.json), for a self-hosted instance's own address.
//
// Exported, a method rather than a free function taking *assetPipeline
// (cleanup 3.2 slice 2a): moved verbatim from hack_get_started.go, which
// (moving) reaches it through hackPages.
func (p *assetPipeline) RewriteHackPluginZip(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		src, err := f.Open()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(src)
		src.Close()
		if err != nil {
			return nil, err
		}
		switch filepath.Ext(f.Name) {
		case ".md", ".yaml", ".json":
			body = p.HackInstanceText(body)
		}
		dst, err := zw.Create(f.Name)
		if err != nil {
			return nil, err
		}
		if _, err = dst.Write(body); err != nil {
			return nil, err
		}
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// hack-connect-steps.json is the one source for how each AI app connects to
// Simple Hack. /get-started renders every app's list from it
// (expandConnectSteps, called from chrome.go's chromePageRaw: the page
// marks each spot with <!--sh:connect:ID-->), and the Website screen
// (participant-website.js) fetches the same file raw, shows the apps marked
// "tab" with the shared publish steps below, and names the signed-in email
// in signin_hint. html, after and fallback are our own trusted markup,
// never user input.
//
// Moved verbatim from hack_connect_steps.go (cleanup 3.2 slice 2a):
// chrome.go (staying) calls expandConnectSteps.

// ConnectCopy is an extra copyable value inside a step (a client id, an
// install command).
type ConnectCopy struct {
	ID    string `json:"id"`
	Value string `json:"value"`
	Label string `json:"label"`
}

// ConnectStep is one numbered step.
//
// Command is a one-line terminal command template, with a literal "{address}"
// placeholder for the MCP address (e.g. "claude mcp add --transport http
// simple-hack {address}"). It renders as its own copy box, filled server-side
// from doc.MCPAddress and client-side (hack-get-started.html's own script,
// and participant-website.js/account.js) from the viewer's own origin, same
// as an Address step's copy box.
type ConnectStep struct {
	HTML         string       `json:"html"`
	Address      bool         `json:"address,omitempty"`
	AddressLabel string       `json:"address_label,omitempty"`
	Copy         *ConnectCopy `json:"copy,omitempty"`
	Command      string       `json:"command,omitempty"`
	After        string       `json:"after,omitempty"`
	Prompt       bool         `json:"prompt,omitempty"`
}

// ConnectApp is one AI app's connect steps.
//
// ConnectHosted, when set, is an alternate steps list (a directory/browse
// path, e.g. Claude's connector directory) that only the canonical hosted
// instance can offer. Neither expandConnectSteps nor connectStepsListHTML
// (this file, server-rendered /get-started) read it — that page already
// picks hosted vs. self with its own <!--sh:hosted-->/<!--sh:self-->
// markers around the markup, same as every other hosted-only block. It
// exists so the Website and Account screens' client-side renderers
// (hack/connect-steps.js, hack/screens/participant-website.js), which have
// no server-side hosted/self marker to lean on, can pick the same way the
// page does (by the viewer's own origin) without duplicating the whole
// steps array per screen.
type ConnectApp struct {
	ID            string        `json:"id"`
	Label         string        `json:"label"`
	Tab           bool          `json:"tab,omitempty"`
	Connect       []ConnectStep `json:"connect"`
	ConnectHosted []ConnectStep `json:"connect_hosted,omitempty"`
	Fallback      string        `json:"fallback,omitempty"`
	Note          string        `json:"note,omitempty"`
}

// ConnectStepsDoc is the whole hack-connect-steps.json file.
type ConnectStepsDoc struct {
	V          int           `json:"v"`
	MCPPath    string        `json:"mcp_path"`
	MCPAddress string        `json:"mcp_address"`
	SigninHint string        `json:"signin_hint"`
	Publish    []ConnectStep `json:"publish"`
	Apps       []ConnectApp  `json:"apps"`
}

var (
	connectStepsOnce sync.Once
	connectStepsDoc  ConnectStepsDoc
	connectStepsErr  error
)

// loadConnectSteps parses the embedded hack-connect-steps.json once. The
// served copy (static file server) and this copy are the same bytes.
func loadConnectSteps() (ConnectStepsDoc, error) {
	connectStepsOnce.Do(func() {
		raw, err := embeddedStatic.ReadFile("static/hack-connect-steps.json")
		if err != nil {
			connectStepsErr = err
			return
		}
		connectStepsErr = json.Unmarshal(raw, &connectStepsDoc)
	})
	return connectStepsDoc, connectStepsErr
}

// connectAppByID finds one app's steps.
func connectAppByID(id string) (ConnectApp, bool) {
	doc, err := loadConnectSteps()
	if err != nil {
		return ConnectApp{}, false
	}
	for _, a := range doc.Apps {
		if a.ID == id {
			return a, true
		}
	}
	return ConnectApp{}, false
}

func connectCopyRow(id, value, label string) string {
	return `<div class="copyrow"><code id="` + html.EscapeString(id) + `">` + html.EscapeString(value) +
		`</code><button class="btn" type="button" data-copy-target="` + html.EscapeString(id) +
		`" aria-label="` + html.EscapeString(label) + `">Copy</button></div>`
}

// connectAddressRow is connectCopyRow for the one MCP address, marked
// data-mcp-address so the page's own script can refill it from the
// viewer's origin (exact on a self-hosted instance too, not just
// HackInstanceText's string rewrite).
func connectAddressRow(id, value, label string) string {
	return `<div class="copyrow"><code id="` + html.EscapeString(id) + `" data-mcp-address>` + html.EscapeString(value) +
		`</code><button class="btn" type="button" data-copy-target="` + html.EscapeString(id) +
		`" aria-label="` + html.EscapeString(label) + `">Copy</button></div>`
}

// connectCommandRow renders a ConnectStep.Command template, filled with
// mcpAddress, in its own copy box. data-mcp-cmd carries the raw template
// (its one "{address}" placeholder) so the page's own script can refill it
// from the viewer's origin, the same way data-mcp-address does for a plain
// address row.
func connectCommandRow(id, template, mcpAddress, label string) string {
	filled := strings.ReplaceAll(template, "{address}", mcpAddress)
	return `<div class="copyrow"><code id="` + html.EscapeString(id) + `" data-mcp-cmd="` + html.EscapeString(template) + `">` + html.EscapeString(filled) +
		`</code><button class="btn" type="button" data-copy-target="` + html.EscapeString(id) +
		`" aria-label="` + html.EscapeString(label) + `">Copy</button></div>`
}

// connectStepsListHTML is one app's <ol class="steps"> for /get-started.
// The address is the hosted one; HackInstanceText rewrites it on another
// server exactly as it did when the list was written into the page, and the
// page's own script refills it (and any command box) from the viewer's
// origin so it is always exact.
func connectStepsListHTML(doc ConnectStepsDoc, app ConnectApp) string {
	var b strings.Builder
	b.WriteString(`<ol class="steps">`)
	for _, s := range app.Connect {
		b.WriteString("\n            <li>" + s.HTML)
		if s.Copy != nil {
			b.WriteString(connectCopyRow(s.Copy.ID, s.Copy.Value, s.Copy.Label))
		}
		if s.Address {
			label := s.AddressLabel
			if label == "" {
				label = app.Label
			}
			b.WriteString("\n              " + connectAddressRow("mcp-"+app.ID, doc.MCPAddress, "Copy "+label+" connector address") + "\n            ")
		}
		if s.Command != "" {
			b.WriteString("\n              " + connectCommandRow("cmd-"+app.ID, s.Command, doc.MCPAddress, "Copy "+app.Label+" command") + "\n            ")
		}
		b.WriteString(s.After + "</li>")
	}
	b.WriteString("\n          </ol>")
	return b.String()
}

// expandConnectSteps replaces each <!--sh:connect:ID--> in a page with that
// app's list. A marker for an app the file lacks is left as an HTML comment
// (invisible) rather than failing the page; TestHackGetStartedOnboarding
// catches a missing list.
func expandConnectSteps(page []byte) []byte {
	if !bytes.Contains(page, []byte("<!--sh:connect:")) {
		return page
	}
	doc, err := loadConnectSteps()
	if err != nil {
		return page
	}
	for _, app := range doc.Apps {
		page = bytes.ReplaceAll(page, []byte("<!--sh:connect:"+app.ID+"-->"), []byte(connectStepsListHTML(doc, app)))
		if app.Fallback != "" {
			page = bytes.ReplaceAll(page, []byte("<!--sh:connect-fallback:"+app.ID+"-->"), []byte(`<p class="hint">`+app.Fallback+`</p>
          `+connectCopyRow("mcp-"+app.ID, doc.MCPAddress, "Copy "+app.Label+" connector address")))
		}
	}
	return page
}

// Moved verbatim from hack_docs.go (cleanup 3.2 slice 2a): ui.go,
// chrome.go and instancehost.go (all staying) call these; none of it is
// Hack-HTTP transport code, just shared OpenAPI-contract filtering and two
// small HTML text patches.

// hackOpenAPISpec derives the hosted Hack reference from the shared contract.
// JSON is valid YAML 1.2; both spec URLs serve the same filtered document.
func hackOpenAPISpec(source []byte) ([]byte, error) {
	return publicOpenAPISpec(source, "public-hack")
}

// publicOpenAPISpec selects operations from the single contract by audience
// (webassets.PublicOpenAPISpec, shared with the docs reference generator).
func publicOpenAPISpec(source []byte, audience string) ([]byte, error) {
	return webassets.PublicOpenAPISpec(source, audience)
}

func openAPIMethod(method string) bool { return webassets.OpenAPIMethod(method) }

// hackPersonalPresentationHTML: shared help pages omit personal
// presentation when served on Hack.
func hackPersonalPresentationHTML(name string, body []byte) []byte {
	switch name {
	case "features.html":
		body = regexp.MustCompile(`<a data-host-only [^>]*>[^<]*</a>`).ReplaceAll(body, nil)
		// The page is generated from features.json; each feature is one <details>.
		start := bytes.Index(body, []byte(`<details class="feat" id="your-home-page">`))
		if start >= 0 {
			end := bytes.Index(body[start:], []byte("</details>"))
			if end >= 0 {
				return append(append([]byte{}, body[:start]...), body[start+end+len("</details>"):]...)
			}
		}
	case "architecture.html":
		return bytes.ReplaceAll(body, []byte("showcase or selected home, with a live feed, bio, pins and order"), []byte("showcase"))
	}
	return body
}
