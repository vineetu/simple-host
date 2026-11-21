package accesstest

import (
	"testing"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/web"
)

// withEmailSignIn flips the package-level EMAIL_SIGNIN switch for the
// duration of a test and restores it afterward, so tests never leak state
// into one another (the switch is a single process-wide var).
func withEmailSignIn(t *testing.T, on bool) {
	t.Helper()
	previous := web.EmailSignInEnabled
	accesshttp.SetEmailSignIn(on)
	t.Cleanup(func() { accesshttp.SetEmailSignIn(previous) })
}
