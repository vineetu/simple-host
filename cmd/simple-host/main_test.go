package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestRefuseEventsProduct(t *testing.T) {
	for _, events := range []string{"hosted", " HOSTED "} {
		t.Run(events, func(t *testing.T) {
			t.Setenv("EVENTS", events)
			var output bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&output)
			t.Cleanup(func() { log.SetOutput(previous) })
			if code := run(nil); code == 0 {
				t.Fatal("accepted EVENTS=hosted")
			}
			if !strings.Contains(output.String(), "this program does not include an events product; unset EVENTS") {
				t.Fatal(output.String())
			}
		})
	}
}
