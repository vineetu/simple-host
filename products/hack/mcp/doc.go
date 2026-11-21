// Package hackmcp declares Simple Hack's AI tools and instructions. The tool
// server (host/mcp) receives them as one Contribution (contribution.go).
//
// Adding a tool: declare it beside its neighbours (hack_entry_tools.go,
// hack_organiser_tools.go, ...), calling the Hack REST route; add its row to
// hackAnnotationTable (annotations_test.go) and its entry to
// plugins/openai-hack/chatgpt-app-submission.json. The Simple Host tools this
// connection also serves are the hostTools list in contribution.go.
package hackmcp
