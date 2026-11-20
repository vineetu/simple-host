package hackevents

import "github.com/vsriram/simple-host/products/hack/rules/hackevents"

// TeamRefusal is the status and message for a TeamWriteState refusal code.
func TeamRefusal(code string) (int, string) {
	return hackevents.TeamRefusalStatus(code), hackevents.TeamRefusalMessage(code)
}
