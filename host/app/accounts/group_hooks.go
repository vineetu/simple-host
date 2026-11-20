package accounts

// GroupHooks are the product's hooks into accounts: Erasure, for an account
// whose deletion the product must answer first.
type GroupHooks struct {
	Erasure Erasure
}

var groupHooks GroupHooks

// SetGroupHooks installs the process's product hooks before services or request
// handlers start. Services read them at call time rather than copying them.
func SetGroupHooks(hooks GroupHooks) { groupHooks = hooks }
