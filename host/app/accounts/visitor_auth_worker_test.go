package accounts

import (
	"context"
	"testing"
)

// Disabled account storage must leave the expiry owner safe to stop and join,
// including the bare-handler case that predates service injection.
func TestVisitorAuthWorkerWithoutDatabase(t *testing.T) {
	for _, service := range []*Service{nil, New(nil)} {
		owner := NewVisitorAuthWorker(service)
		owner.Start(context.Background())
		owner.Stop()
		owner.Wait()
	}
}
