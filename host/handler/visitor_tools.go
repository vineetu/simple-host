package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/datahttp"
	httpx "github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/mcp"
)

// VisitorTools answers the connector's visitor tools (visitor_read_site,
// visitor_add_entry, visitor_change_entry). Access admits the person to the
// site as a visitor (account, address, named viewers, passcode); the site's
// saved data is then read or written with visitor rights only, through the
// same storage routes a browser on the site uses.
type VisitorTools struct {
	Access *accesshttp.AccessHandler
	Data   *datahttp.DataHandler
	oauth  *accesshttp.OAuthHandler
}

// SetVisitorTools gives the connector its visitor tools. They use the same
// address validator as browser sign-in; no connector token or owner
// credential reaches a visitor request.
func SetVisitorTools(c *accesshttp.ConnectorHandler, a *accesshttp.AccessHandler, d *datahttp.DataHandler, oauth *accesshttp.OAuthHandler) {
	c.SetVisitorTool(VisitorTools{Access: a, Data: d, oauth: oauth}.call)
}

// call is one visitor tool call: no address lists the sites shared with the
// person; otherwise the site is opened as a visitor and read or written.
func (t VisitorTools) call(orig *http.Request, caller mcp.Caller, tool string, args map[string]any) (map[string]any, error) {
	user, err := t.Access.VisitorToolUser(orig.Context(), caller)
	if err != nil {
		return nil, err
	}
	address, err := accesshttp.VisitorString(args, "address")
	if err != nil {
		return nil, err
	}
	if address == "" {
		return t.Access.VisitorSitesList(orig.Context(), tool, user.ID)
	}
	v, err := t.Access.OpenVisitorSite(orig, t.oauth, user.ID, caller.AppName, address)
	if err != nil {
		return nil, err
	}
	defer v.Revoke()
	if err := t.unlock(&v, args); err != nil {
		return nil, err
	}
	var result any
	if tool == "visitor_read_site" {
		result, err = t.Read(v.Req, v.SiteID, v.OwnerID, v.SiteName, args)
	} else {
		result, err = t.write(v.Req, v.SiteID, user.ID, caller.AppName, tool, args)
	}
	if err != nil {
		return nil, err
	}
	t.Access.RecordVisitorSite(v.Req, caller, v.SiteID)
	return accesshttp.VisitorResult(result, user.ID == v.OwnerID), nil
}

// unlock asks Access for the site's passcode unless the call names a single
// resource, and no page, that is known and explicitly exempt from it. A
// resource that does not exist on an open site is refused here.
func (t VisitorTools) unlock(v *accesshttp.VisitorVisit, args map[string]any) error {
	resource, err := accesshttp.VisitorString(args, "resource")
	if err != nil {
		return err
	}
	page, err := accesshttp.VisitorString(args, "page")
	if err != nil {
		return err
	}
	needsPasscode := true
	if resource != "" && page == "" {
		x, err := t.Data.LoadStorageResource(v.Req.Context(), v.SiteID, resource)
		if err != nil && v.Passcode.Enc == nil {
			return fmt.Errorf("resource not found")
		}
		// A missing resource remains protected until the caller unlocks the
		// site. Only a known, explicitly exempt resource skips that check.
		needsPasscode = err != nil || x.SitePasscode != "off"
	}
	if v.Passcode.Enc == nil || !needsPasscode {
		return nil
	}
	return t.Access.UnlockVisitorSite(v, args)
}

// Read is visitor_read_site: the page's text, the named resource's entries,
// and every resource this visitor has rights on (with a first page of what
// they may read when no resource was named).
func (t VisitorTools) Read(r *http.Request, siteID, ownerID, siteName string, args map[string]any) (map[string]any, error) {
	resource, err := accesshttp.VisitorString(args, "resource")
	if err != nil {
		return nil, err
	}
	page, err := accesshttp.VisitorString(args, "page")
	if err != nil {
		return nil, err
	}
	if _, err := visitorLimit(args); err != nil {
		return nil, err
	}
	out := map[string]any{}
	if page != "" {
		text, truncated, err := t.Access.VisitorPageText(r, siteID, ownerID, siteName, page)
		if err != nil {
			return nil, err
		}
		out["page_text"], out["truncated"] = text, truncated
	}
	if resource != "" {
		data, err := t.resourceRequest(r, args, "read")
		if err != nil {
			return nil, err
		}
		out["entries"] = data
	}
	list, err := t.visibleResources(r, siteID, ownerID, siteName, resource)
	if err != nil {
		return nil, err
	}
	if resource == "" {
		if err := t.fillFirstPages(r, list, args); err != nil {
			return nil, err
		}
	}
	out["resources"] = list
	return out, nil
}

// visibleResources lists the site's KV and SQLite resources (only the named
// one, when set) with this visitor's rights, skipping those with none.
func (t VisitorTools) visibleResources(r *http.Request, siteID, ownerID, siteName, only string) ([]map[string]any, error) {
	resources, err := t.Data.ListStorageResourceRows(r.Context(), siteID)
	if err != nil {
		return nil, err
	}
	base := datahttp.StorageCall{SiteID: siteID, OwnerID: ownerID, SiteName: siteName, VisitorAI: r.Header.Get(accesshttp.VisitorPassHeader) != ""}
	t.Access.StorageVisitor(r, &base)
	base.RuntimeDir = t.Data.StorageRuntimeDir(base)
	list := []map[string]any{}
	for _, x := range resources {
		if only != "" && only != x.Name || x.Kind == "files" {
			continue
		}
		c := base
		c.ResourceName, c.Resource = x.Name, x
		entries, err := t.resourceEntries(r, c)
		if err != nil {
			return nil, err
		}
		list = append(list, entries...)
	}
	return list, nil
}

// resourceEntries is one resource's listing: a KV namespace, or each table
// of a database the visitor has rights on.
func (t VisitorTools) resourceEntries(r *http.Request, c datahttp.StorageCall) ([]map[string]any, error) {
	svc := t.Data.StorageFor(c)
	c.OwnerOnSite = svc.OwnerOnSite(r.Context(), c)
	x := c.Resource
	if x.Kind == "sqlite" && len(x.Tables) == 0 || x.Kind == "kv" {
		// With no table overrides the database rules decide every table:
		// a database that gives this visitor nothing is never opened.
		rights, err := svc.RuleRights(r.Context(), c, "")
		if err != nil || !visitorRightsAny(rights) {
			return nil, err
		}
		if x.Kind == "kv" {
			return []map[string]any{{"resource": x.Name, "kind": x.Kind, "rights": rights}}, nil
		}
	}
	if x.Kind != "sqlite" {
		return nil, nil
	}
	return t.tableEntries(r, c, svc)
}

// tableEntries opens a database and lists each table the visitor has rights
// on, with its columns when they may read or add.
func (t VisitorTools) tableEntries(r *http.Request, c datahttp.StorageCall, svc *appstorage.Service) ([]map[string]any, error) {
	list := []map[string]any{}
	rec := httptest.NewRecorder()
	ok := t.Data.InspectStorage(rec, r, c, false, func(conn datahttp.StorageInspection) error {
		tables, err := datahttp.StorageSQLTables(conn)
		if err != nil {
			return err
		}
		names := []string{}
		for name := range tables {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, table := range names {
			entry, err := t.tableEntry(r, c, svc, conn, table)
			if err != nil {
				return err
			}
			if entry != nil {
				list = append(list, entry)
			}
		}
		return nil
	})
	if !ok {
		return nil, accesshttp.VisitorResponseError(rec)
	}
	return list, nil
}

// tableEntry is one table's listing, nil when the visitor has no rights on it.
func (t VisitorTools) tableEntry(r *http.Request, c datahttp.StorageCall, svc *appstorage.Service, conn datahttp.StorageInspection, table string) (map[string]any, error) {
	rights, err := svc.RuleRights(r.Context(), c, table)
	if err != nil || !visitorRightsAny(rights) {
		return nil, err
	}
	entry := map[string]any{"resource": c.Resource.Name, "kind": c.Resource.Kind, "table": table, "rights": rights}
	if rights["read"] != true && rights["add"] != true {
		return entry, nil
	}
	cols, _, err := datahttp.StorageTableInfo(conn, table)
	if err != nil {
		return nil, err
	}
	columns := make(map[string]string, len(cols))
	for name, typ := range cols {
		name, err = t.Access.VisitorStructuralName(r, name)
		if err != nil {
			return nil, err
		}
		columns[name] = typ
	}
	entry["columns"] = accesshttp.VisitorStructure{Value: columns}
	return entry, nil
}

// fillFirstPages adds one bounded page of data to each readable resource.
func (t VisitorTools) fillFirstPages(r *http.Request, list []map[string]any, args map[string]any) error {
	for _, item := range list {
		if item["rights"].(map[string]any)["read"] != true {
			continue
		}
		query := map[string]any{"resource": item["resource"], "kind": item["kind"]}
		if limit, ok := args["limit"]; ok {
			query["limit"] = limit
		}
		if item["table"] != nil {
			query["table"] = item["table"]
		}
		data, err := t.resourceRequest(r, query, "read")
		if err != nil {
			return err
		}
		item["entries"] = data
	}
	return nil
}

// write is visitor_add_entry and visitor_change_entry, within the day's cap.
func (t VisitorTools) write(r *http.Request, siteID, userID, app, tool string, args map[string]any) (any, error) {
	action := "add"
	if tool == "visitor_change_entry" {
		var err error
		action, err = accesshttp.VisitorString(args, "action")
		if err != nil {
			return nil, err
		}
		if action != "edit" && action != "delete" {
			return nil, fmt.Errorf("action must be edit or delete")
		}
	}
	resource, _ := accesshttp.VisitorString(args, "resource")
	kind, _ := accesshttp.VisitorString(args, "kind")
	return t.Data.StorageSvc().VisitorToolWrite(r.Context(), appstorage.VisitorToolWriteInput{
		SiteID: siteID, UserID: userID, App: app, Action: action,
		Resource: resource, Kind: kind, Cap: t.Access.Limits.VisitorAIWritesPerDay,
		Write: func(action string) (any, error) {
			return t.resourceRequest(r, args, action)
		},
	})
}

// visitorMethods is the storage route method for each visitor action.
var visitorMethods = map[string]string{"read": "GET", "add": "POST", "edit": "PATCH", "delete": "DELETE"}

// resourceRequest replays one visitor action on a resource as the storage
// route a browser would call, with the visitor's pass.
func (t VisitorTools) resourceRequest(base *http.Request, args map[string]any, action string) (any, error) {
	resource, err := accesshttp.VisitorString(args, "resource")
	if err != nil {
		return nil, err
	}
	kind, err := accesshttp.VisitorString(args, "kind")
	if err != nil {
		return nil, err
	}
	if !appstorage.ValidResourceName(resource) {
		return nil, fmt.Errorf("invalid resource name")
	}
	r := base.Clone(base.Context())
	r.SetPathValue("name", resource)
	r.Header.Set("X-SH-Visitor-Action", action)
	// Set the path as a normal storage request so per-visitor rate limits and
	// idempotency classify it correctly; path values are set by this adapter.
	r.URL.Path = "/v1/sites/" + r.PathValue("sitename") + "/storage/" + kind + "/" + resource
	q, err := t.pageQuery(base, args, kind, resource)
	if err != nil {
		return nil, err
	}
	r.URL.RawQuery = q.Encode()
	method := visitorMethods[action]
	if method == "" {
		return nil, fmt.Errorf("action must be edit or delete")
	}
	value, hasValue := args["value"]
	if (action == "add" || action == "edit") && !hasValue {
		return nil, fmt.Errorf("value is required")
	}
	switch kind {
	case "kv":
		return t.kvRequest(r, args, action, method, value)
	case "sqlite":
		return t.rowsRequest(r, args, action, method, value)
	default:
		return nil, fmt.Errorf("kind must be kv or sqlite")
	}
}

// pageQuery is the limit and, for a later page, the cursor: a KV cursor was
// sealed for this site and resource when it was handed out.
func (t VisitorTools) pageQuery(base *http.Request, args map[string]any, kind, resource string) (url.Values, error) {
	q := url.Values{}
	limit, err := visitorLimit(args)
	if err != nil {
		return nil, err
	}
	q.Set("limit", strconv.Itoa(limit))
	after, err := accesshttp.VisitorString(args, "after")
	if err != nil || after == "" {
		return q, err
	}
	if kind == "kv" {
		pass, _ := t.Access.StoreAccounts().LookupInternalVisitorKey(base.Header.Get(accesshttp.VisitorPassHeader))
		raw, err := httpx.OpenLink(t.Access.VisitorRefKey(), "visitor-kv-cursor:"+pass.SiteID+":"+resource, after)
		if err != nil {
			return nil, fmt.Errorf("invalid pagination cursor")
		}
		after = string(raw)
	}
	q.Set("after", after)
	q.Set("before", after)
	return q, nil
}

// kvRequest is a visitor action on one KV key (add and edit both put it).
func (t VisitorTools) kvRequest(r *http.Request, args map[string]any, action, method string, value any) (any, error) {
	key, err := accesshttp.VisitorString(args, "key")
	if err != nil {
		return nil, err
	}
	if action != "read" && key == "" {
		return nil, fmt.Errorf("key is required")
	}
	r.SetPathValue("key", key)
	if action == "add" || action == "edit" {
		method = "PUT"
	}
	result, err := VisitorInvoke(r, method, map[string]any{"value": value}, t.Data.StorageRouteFor(t.Access.StorageCaller, t.Data.StorageKV).ServeHTTP)
	if err != nil {
		return nil, err
	}
	return t.Access.VisitorKVResult(r, r.PathValue("name"), result)
}

// rowsRequest is a visitor action on a table's rows (one row, for edit and
// delete); identity columns are blanked in what comes back.
func (t VisitorTools) rowsRequest(r *http.Request, args map[string]any, action, method string, value any) (any, error) {
	table, err := accesshttp.VisitorString(args, "table")
	if err != nil {
		return nil, err
	}
	if !appstorage.TableNameRE.MatchString(table) {
		return nil, fmt.Errorf("table is required")
	}
	r.SetPathValue("table", table)
	if err := setVisitorRowID(r, args, action); err != nil {
		return nil, err
	}
	result, err := VisitorInvoke(r, method, value, t.Data.StorageRouteFor(t.Access.StorageCaller, t.Data.StorageRows).ServeHTTP)
	if obj, ok := result.(map[string]any); ok {
		if err := t.Access.ScrubVisitorRows(r, obj); err != nil {
			return nil, err
		}
	}
	return result, err
}

// setVisitorRowID puts the entry id on the route: never for an add, always
// for an edit or delete.
func setVisitorRowID(r *http.Request, args map[string]any, action string) error {
	v, has := args["id"]
	if action == "add" && has {
		return fmt.Errorf("id is assigned when the entry is added")
	}
	if has {
		n, err := VisitorEntryID(v)
		if err != nil {
			return err
		}
		r.SetPathValue("id", strconv.FormatInt(n, 10))
	}
	if (action == "edit" || action == "delete") && r.PathValue("id") == "" {
		return fmt.Errorf("id is required")
	}
	return nil
}

func visitorRightsAny(r map[string]any) bool {
	return r["read"] == true || r["add"] == true || r["edit_own"] == true || r["delete_own"] == true
}

func visitorLimit(args map[string]any) (int, error) {
	if v, ok := args["limit"]; ok {
		n, err := VisitorEntryID(v)
		if err != nil || n > 500 {
			return 0, fmt.Errorf("limit must be a whole number from 1 to 500")
		}
		return int(n), nil
	}
	return 50, nil
}

func VisitorEntryID(v any) (int64, error) {
	raw, ok := v.(json.Number)
	n, err := raw.Int64()
	if !ok || err != nil || n < 1 {
		return 0, fmt.Errorf("id must be a positive whole number")
	}
	return n, nil
}

// VisitorInvoke never accepts a caller-supplied route or credential.
func VisitorInvoke(base *http.Request, method string, body any, fn http.HandlerFunc) (any, error) {
	r := base.Clone(base.Context())
	r.Method = method
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	fn(rec, r)
	if rec.Code < 200 || rec.Code >= 300 {
		return nil, accesshttp.VisitorResponseError(rec)
	}
	var result any
	dec := json.NewDecoder(rec.Body)
	dec.UseNumber()
	if err := dec.Decode(&result); err != nil {
		return nil, fmt.Errorf("visitor request returned no data")
	}
	return result, nil
}
