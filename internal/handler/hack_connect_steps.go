package handler

import (
	"bytes"
	"encoding/json"
	"html"
	"strings"
	"sync"
)

// hack-connect-steps.json is the one source for how each AI app connects to
// Simple Hack. /get-started renders every app's list from it
// (expandConnectSteps, called from chromePageRaw: the page marks each spot
// with <!--sh:connect:ID-->), and the Website screen (participant-website.js)
// fetches the same file raw, shows the apps marked "tab" with the shared
// publish steps below, and names the signed-in email in signin_hint. html,
// after and fallback are our own trusted markup, never user input.

// ConnectCopy is an extra copyable value inside a step (a client id, an
// install command).
type ConnectCopy struct {
	ID    string `json:"id"`
	Value string `json:"value"`
	Label string `json:"label"`
}

// ConnectStep is one numbered step.
type ConnectStep struct {
	HTML         string       `json:"html"`
	Address      bool         `json:"address,omitempty"`
	AddressLabel string       `json:"address_label,omitempty"`
	Copy         *ConnectCopy `json:"copy,omitempty"`
	After        string       `json:"after,omitempty"`
	Prompt       bool         `json:"prompt,omitempty"`
}

// ConnectApp is one AI app's connect steps.
type ConnectApp struct {
	ID       string        `json:"id"`
	Label    string        `json:"label"`
	Tab      bool          `json:"tab,omitempty"`
	Connect  []ConnectStep `json:"connect"`
	Fallback string        `json:"fallback,omitempty"`
	Note     string        `json:"note,omitempty"`
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

// connectStepsListHTML is one app's <ol class="steps"> for /get-started.
// The address is the hosted one; hackInstanceText rewrites it on another
// server exactly as it did when the list was written into the page.
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
			b.WriteString("\n              " + connectCopyRow("mcp-"+app.ID, doc.MCPAddress, "Copy "+label+" connector address") + "\n            ")
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
