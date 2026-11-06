package mcp

// Output schemas: what each tool returns in structuredContent on success.
//
// A client that knows a tool's outputSchema may validate every result against
// it and reject a result that does not conform, so each schema here describes
// exactly what the tool's run function builds: the same property names, the
// same optional fields, the same types. A property is required only when the
// tool always sets it. additionalProperties is false wherever the tool builds
// the object itself; values the tool passes through from visitors or pages
// (saved state, collection items) are left open.
//
// Error results (isError: true) carry no structuredContent, so they are not
// held to these schemas. TestOutputSchemasMatchRealResults (internal/handler)
// drives every tool against the real application and validates its output.
//
// additionalProperties: false is what lets a schema catch a tool that starts
// returning a field it never declared -- exactly what every test above does,
// against the schema as authored here. It is also, unhelpfully, what makes a
// schema brittle for a real client: a client that cached an older copy of a
// tool's outputSchema and validates its own results against it will reject a
// later, purely additive field as a failure even though the call succeeded
// (SIM-384: hack_create_event grew judging_ends_at and a cached client
// rejected the create it had just made). The fix is not to stop authoring
// closed schemas -- that is still what catches drift in tests -- but to open
// them only on the copy a client actually sees: Tool.MarshalJSON
// (tools.go) runs every OutputSchema through openOutputSchemaForWire before
// it goes out in tools/list, turning every additionalProperties: false into
// true, recursively. A test that reads tool.OutputSchema directly (every
// test in this package) still sees the authored, closed map.

func outObject(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// openOutputSchemaForWire returns a deep copy of schema with every
// additionalProperties: false opened to true, at every nesting level, so a
// client holding an older cached copy of this outputSchema never turns a
// later, additive field into a rejected call (SIM-384). See the package
// comment above. Only Tool.MarshalJSON calls this, so nothing in this
// package or internal/handler that validates against tool.OutputSchema
// directly is affected; those keep checking the schema as authored.
func openOutputSchemaForWire(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		if k == "additionalProperties" {
			if closed, ok := v.(bool); ok && !closed {
				out[k] = true
				continue
			}
		}
		out[k] = openSchemaValueForWire(v)
	}
	return out
}

func openSchemaValueForWire(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return openOutputSchemaForWire(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = openSchemaValueForWire(e)
		}
		return out
	default:
		return v
	}
}

// pageWarningsSchema describes deploySite's page checks (pagecheck.go).
var pageWarningsSchema = map[string]any{"type": "array", "items": map[string]any{"type": "string"},
	"description": "Problems found in the published pages that stop them signing in or saving (auth.js missing, a storage resource or table the site does not have, a double-escaped script). Present only when there are some; fix them before telling the person the page works."}

func outString(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func outInteger(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func outBool(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func outEnum(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": description}
}

func outArray(description string, items map[string]any) map[string]any {
	return map[string]any{"type": "array", "description": description, "items": items}
}

// anyJSON is a value of any JSON type: what a site's pages or visitors saved.
func anyJSON(description string) map[string]any {
	return map[string]any{"description": description}
}

func withDescription(schema map[string]any, description string) map[string]any {
	schema["description"] = description
	return schema
}

const (
	outSiteName   = "The site's name, as used in its address and in other tools' `site` argument."
	outCollection = "The collection's name."
)

// siteSummaryProperties are the fields restSite.summary sets.
func siteSummaryProperties() map[string]any {
	return map[string]any{
		"summary": outObject(map[string]any{
			"title":       outString("Page title."),
			"description": outString("Meta description."),
			"headings":    outString("First six headings."),
			"text":        outString("Short visible text excerpt."),
		}),
		"file_bytes":     outInteger("Website files including current and kept versions."),
		"name":           outString(outSiteName),
		"url":            outString("The site's live public address: its connected domain once that is active, else its address under the account's address family (e.g. https://<site>.trips.example.com/) when one serves it, otherwise https://<site>.<handle>.simple-host.app/ (or, briefly for a new account, https://<handle>.simple-host.app/<site>/). Give the person this exact value."),
		"active_version": outInteger("The version number visitors see now (0 if nothing is published yet)."),
		"listed":         outBool("Whether the site is listed on the account's public page. An unlisted site is still public to anyone with its address."),
		"custom_domain":  outString("The site's own domain, present only when one is connected."),
		"address_note":   outString("Present while the site is at its fallback https://<handle>.simple-host.app/<site>/ because the owner's own address is not ready yet: when it moves, roughly how long, and that visitors' sign-ins and browser-kept data start fresh. Pass it on when handing out the address."),
		"domain_status":  outEnum("State of the custom domain, present only with custom_domain: pending (DNS not proven yet), active (serving), error (resolves here but HTTPS fails).", "pending", "active", "error"),
	}
}

var siteSummaryRequired = []string{"name", "url", "active_version", "listed"}

func siteSummarySchema() map[string]any {
	return outObject(siteSummaryProperties(), siteSummaryRequired...)
}

// siteSummaryWith is the summary plus extra fields the tool adds.
func siteSummaryWith(extra map[string]any, extraRequired ...string) map[string]any {
	props := siteSummaryProperties()
	for k, v := range extra {
		props[k] = v
	}
	return outObject(props, append(append([]string{}, siteSummaryRequired...), extraRequired...)...)
}

// trafficSplitSchema mirrors the analytics Split: one bucket of traffic, by
// class, as views and unique visitors.
func trafficSplitSchema(description string) map[string]any {
	counts := func(desc string) map[string]any {
		return withDescription(outObject(map[string]any{
			"views":    outInteger("Page views."),
			"visitors": outInteger("Unique visitors."),
		}, "views", "visitors"), desc)
	}
	return withDescription(outObject(map[string]any{
		"person":  counts("Real people, not counting the owner's own visits from a device they used while signed in. Report these when asked how many people visited."),
		"bot":     counts("Crawlers, scanners and other automated clients."),
		"infra":   counts("Simple Host's own health checks and monitoring."),
		"unknown": counts("Traffic from before visits were classified; not split by kind."),
	}, "person", "bot", "infra", "unknown"), description)
}

// domainSchema describes domainSummary. connect_domain (justConnected) always
// answers for the domain it just bound, which is pending or already active and
// has not been checked yet; domain_status can also find no domain at all, or
// one whose last check failed.
func domainSchema(justConnected bool, limits Limits) map[string]any {
	props := map[string]any{
		"site":   outString(outSiteName),
		"domain": map[string]any{"type": []string{"string", "null"}, "description": "The connected domain, or null when the site has none."},
		"status": outEnum("none (no domain), pending (waiting for the DNS record), active (the site is served there), error (resolves here but HTTPS fails), or, for an address family, failing (it stopped passing its checks).", "none", "pending", "active", "error", "failing"),
		"dns_record": withDescription(outObject(map[string]any{
			"type":  outString("Record type to add: CNAME for a subdomain, A for an apex domain."),
			"host":  outString("The name the record is added for."),
			"value": outString("The record's value."),
		}, "type", "host", "value"), "The DNS record that points the domain here, to add at the registrar. Absent for a free simple-host.app address, which needs none."),
		"ownership_record": withDescription(outObject(map[string]any{
			"type":  outString("Always TXT."),
			"host":  outString("_simple-host.<domain>, the name the TXT record is added for."),
			"value": outString("This site's ownership token, the record's value."),
		}, "type", "host", "value"), "The TXT record that proves the domain is the person's, to add at the registrar next to dns_record and keep in place. Nothing is verified or certified without it. Absent for a free simple-host.app address."),
		"partner": withDescription(outObject(map[string]any{
			"domain": outString("www.<domain> for a bare domain, or the bare domain for www.<domain>."),
			"status": outEnum("pending (the domain itself is not live yet), live (it forwards to the domain), or not_set_up (note says why; it is picked up automatically once fixed).", "pending", "live", "not_set_up"),
			"dns_record": withDescription(outObject(map[string]any{
				"type":  outString("CNAME for www.<domain>, A for a bare domain."),
				"host":  outString("The partner name."),
				"value": outString("The record's value."),
			}, "type", "host", "value"), "The record that points the partner here too. The ownership record on the domain covers both."),
			"note": outString("Why the partner is not set up. Present only then."),
		}, "domain", "status", "dns_record"), "The domain's www / bare partner, which forwards to the domain so both work. Present only for a bare domain or www.<bare domain>."),
		"last_check":  outString("Why the domain is not active yet, from the most recent check. Present only after a failed check."),
		"url":         outString("The site's address on this domain. Present only when status is active."),
		"certificate": outEnum("The domain's HTTPS certificate: pending (DNS not pointed here yet), issuing (automatic, usually minutes), live, or failed (last_check says why; it is retried). For an address family, waiting_for_operator: the operator sets up its wildcard certificate. Absent for a free simple-host.app address.", "pending", "issuing", "live", "failed", "waiting_for_operator"),
		"family": withDescription(outObject(map[string]any{
			"site_prefix":      outString("The site-name prefix: <label>.<domain> is the account's site <site_prefix><label>. Empty: <label> is the site's name."),
			"main_address":     outBool("Whether a family address is each matching site's main address (handed out; its own address redirects there), unless the site has a domain of its own."),
			"live":             outBool("Whether the family serves now (verified and its certificate live)."),
			"example_url":      outString("One site's address under the family."),
			"certificate_note": outString("About the certificate, when it is not live."),
		}, "site_prefix", "main_address", "live"), "Present only for an address family (domain *.<domain>): every site of the account answers at <label>.<domain>."),
		"serving_at":    outString("The site's earlier address, where it is still served until this domain is live (then it redirects here). Present only while a new domain is pending."),
		"failing_since": outString("When this verified domain started failing its checks. The owner is emailed after " + span(limits.DomainLapseWarnAfter) + "; after " + span(limits.DomainLapseAfter) + " the domain stops being the site's address."),
		"note":          outString("What to do next. Present only when status is pending."),
	}
	if justConnected {
		props["domain"] = outString("The domain just connected.")
		props["status"] = outEnum("pending (add the DNS records in dns_record and ownership_record, then check with domain_status) or active (a free simple-host.app address, live at once).", "pending", "active")
		delete(props, "last_check")
		delete(props, "failing_since")
		// Just connected: the partner waits with the domain.
		delete(props["partner"].(map[string]any)["properties"].(map[string]any), "note")
	}
	return outObject(props, "domain", "status")
}

// passcodeSchema describes status only; passcodes never enter tool results.
func passcodeSchema(withDate bool) map[string]any {
	props := map[string]any{
		"site":               outString(outSiteName),
		"passcode_protected": outBool("Whether visitors must enter a passcode to open the site now."),
		"note":               outString("A plain note from Simple Host about the change (pass it on to the person)."),
	}
	if withDate {
		props["passcode_set_at"] = outString("When the passcode was set (RFC 3339).")
	}
	return outObject(props, "site", "passcode_protected")
}

// The one display field is offered only with My sites. The cached, flags-off
// inventory and the existing list_sites text retain their original bytes.
func mySitesOutputSchema() map[string]any {
	return outObject(map[string]any{
		"sites": outArray("Sites returned by the requested filters and page.", siteSummaryWith(map[string]any{
			"offline": outBool("The site is offline."),
			"display": outObject(map[string]any{
				"access":             outString("Existing site access value, empty when unknown."),
				"passcode_protected": outBool("Whether opening the site requires a passcode."),
				"updated_at":         outString("Last update in RFC3339."),
				"canonical_url":      outString("The site's own Simple Host address."),
			}, "access", "passcode_protected", "updated_at", "canonical_url"),
		})),
		"count": outInteger("Number of sites returned."),
	}, "sites", "count")
}
