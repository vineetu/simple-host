package signin_test

import (
	"fmt"
	"regexp"
	"sync"
	"testing"

	"simplehost/access/signin"
	"simplehost/core/testkit"
)

// Review A4: guesses sent at the same time cannot pass the five tries a
// code allows; after them the right code no longer works.
func TestCodeTriesHoldUnderParallelGuesses(t *testing.T) {
	k := testkit.Start(t, signin.Mount)
	email := "victim@example.com"
	testkit.Call(t, k.Browser(), "POST", "https://simple-host.app/v1/auth/code", map[string]string{"email": email})
	code := regexp.MustCompile(`\d{6}`).FindString(k.Mail.To(email)[0].Subject)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			guess := fmt.Sprintf("x%05d", i) // never the code
			testkit.Call(t, k.Browser(), "POST", "https://simple-host.app/v1/auth/verify", map[string]string{"email": email, "code": guess},
				"X-Real-IP", fmt.Sprintf("10.0.0.%d", i))
		}(i)
	}
	wg.Wait()
	var tries int
	k.App.DB.QueryRow(`SELECT tries FROM sign_in_codes WHERE email = $1`, email).Scan(&tries)
	if r := testkit.Call(t, k.Browser(), "POST", "https://simple-host.app/v1/auth/verify", map[string]string{"email": email, "code": code}); tries != 5 || r.Status == 200 {
		t.Fatalf("after 40 parallel guesses: %d tries recorded, the right code answers %d", tries, r.Status)
	}
}
