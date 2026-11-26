package mcp

// Observe sets the test hook that sees every route a tool call replays.
func Observe(f func(method, pattern string, body any)) { observe = f }
