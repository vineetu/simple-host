package web

import (
	"github.com/vsriram/simple-host/host/model"
	operations "github.com/vsriram/simple-host/host/ops"
)

// arrivalSource names an outside place that links people in with
// /?from=<key>. Where is the address the link lives on, for the admin page.
type arrivalSource struct {
	Label string `json:"label"`
	Where string `json:"where"`
}

// ArrivalSources is the one list of named sources: the key is both the
// /?from= value and the signup_source recorded. Add a partner by adding a
// line here; the admin page reads its labels from GET /v1/admin/users. Any
// other well-formed from value still counts, as "from:<value>".
var ArrivalSources = map[string]arrivalSource{
	model.SignupArtifactHub: {Label: "Artifact Hub", Where: "artifact-hub.com"},
	model.SignupVercelShip:  {Label: "Vercel Ship", Where: "ship.vineetsriram.com"},
	model.SignupAgentCon:    {Label: "Agent Con", Where: "Agent Con, San Jose"},
}

type SignupGroup = operations.SignupGroup

func CohortSources() map[string]operations.SignupGroup {
	out := make(map[string]operations.SignupGroup, len(ArrivalSources))
	for k, v := range ArrivalSources {
		out[k] = operations.SignupGroup{Key: k, Label: v.Label, Where: v.Where}
	}
	return out
}

func SignupGroupOf(source string) string { return operations.SignupGroupOf(source, CohortSources()) }

func SignupGroups(present map[string]bool) []SignupGroup {
	return operations.SignupGroups(present, CohortSources())
}
