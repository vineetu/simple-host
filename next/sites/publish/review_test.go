package publish_test

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"simplehost/core/limits"
	"simplehost/core/testkit"
	"simplehost/sites/publish"
)

// Round 2: only a few publishes are in flight at once (one per person); a
// body bigger than it may be is refused by its declared size.
func TestPublishesInFlightAreCapped(t *testing.T) {
	k := testkit.Start(t, publish.Mount)
	eve := k.Person(t, "eve")
	k.Publish(t, eve, "evil", map[string]string{"index.html": "x"})
	send := func(body io.Reader, length int64) int {
		req, _ := http.NewRequest("POST", "https://simple-host.app/v1/sites/evil", body)
		req.Header.Set("Authorization", "Bearer "+eve.Key)
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = length
		res, err := k.Browser().Do(req)
		if err != nil {
			return -1
		}
		res.Body.Close()
		return res.StatusCode
	}
	slow, write := io.Pipe()
	first := make(chan int)
	go func() { first <- send(slow, -1) }()
	write.Write([]byte(`{"files":{"index.html":"`))
	busy := 0
	for i := 0; i < 100 && busy != 503; i++ {
		busy = send(strings.NewReader(`{"files":{"index.html":"y"}}`), -1)
		time.Sleep(20 * time.Millisecond)
	}
	write.Write([]byte(`slow"}}`))
	write.Close()
	if s := <-first; busy != 503 || s != 200 {
		t.Fatalf("a second publish while one runs: %d (the first: %d)", busy, s)
	}
	big := bytes.Repeat([]byte("a"), int(limits.SiteBytes.N/3*4+2<<20))
	if s := send(bytes.NewReader(big), int64(len(big))); s != 413 {
		t.Fatalf("declared too big: %d", s)
	}
}
