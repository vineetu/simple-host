package main

import (
	"sync"

	"github.com/vsriram/simple-host/internal/hackhttp"
	"github.com/vsriram/simple-host/internal/webassets"
)

// newAssistantDocsTextFunc returns a events.DocsTextFunc: internal/app/hack/events
// may not import internal/webassets (scripts/layering/rules.txt), so this
// wires webassets' embedded docs tree into hackhttp.AssistantDocsText (the
// one place the HTML-to-text extraction lives -- SIM-394 L22) and injects
// the result into the assistant service. The extraction (parse + strip +
// collapse whitespace for four small pages) runs once, the first time any
// request calls the returned function, and the result is cached for the
// life of the process.
func newAssistantDocsTextFunc() func() string {
	var once sync.Once
	var text string
	return func() string {
		once.Do(func() {
			text = hackhttp.AssistantDocsText(webassets.DocsTree(true))
		})
		return text
	}
}
