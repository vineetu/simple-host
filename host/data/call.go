package data

import "context"
import ruleengine "github.com/vsriram/simple-host/host/storage/rules"

import "github.com/vsriram/simple-host/host/data/rules"

// Call contains the resolved site, caller and policy facts for a storage operation.
type Call struct {
	rulePlan                                *ruleengine.Plan
	ruleSet                                 ruleengine.PolicySet
	ruleValues                              ruleengine.Values
	Context                                 context.Context
	SiteID, OwnerID, SiteName, ResourceName string
	AccountID                               string
	Owner, OwnerOnSite, VisitorAI           bool
	VisitorID, LinkScope                    string
	Resource                                rules.Resource
	ActiveChecked                           bool
	SpamCheck                               string
	CheckedPost                             bool
	RuntimeDir                              string
	VisitorSessionID                        []byte
	VisitorSessionUserID                    string
	CSRFOK                                  bool
	// OwnerFor: resolved and validated by ResolveOwnerFor, never taken
	// directly from a request. Set only for an add by Owner or OwnerOnSite:
	// the server stamps this visitor_id on the new row and marks it
	// written_by the owner, instead of whatever the caller sent.
	OwnerFor string
	// What Access found about a caller who is not the owner. PasscodeOK:
	// the site's passcode lets them in; ViewersOK: its named-viewers rule
	// does; OnOwnAddress: the request came from the site's own address,
	// unframed and not from a preview (the D1 owner-on-site condition);
	// VisitorFamily: the session's sign-out scope for push devices.
	PasscodeOK, ViewersOK, OnOwnAddress bool
	VisitorFamily                       string
}

// callKey carries the Call that Access resolved for a data route on the
// request context (WithCall, CallFrom).
type callKey struct{}

// WithCall returns ctx carrying c, the site and caller Access resolved for a
// data route (AccessHandler.StorageCaller).
func WithCall(ctx context.Context, c Call) context.Context {
	return context.WithValue(ctx, callKey{}, c)
}

// CallFrom is the Call WithCall put on ctx; the zero Call when there is none.
func CallFrom(ctx context.Context) Call {
	c, _ := ctx.Value(callKey{}).(Call)
	return c
}
