// Package events hands an event organiser names under the event domains this
// instance owns (<name>.<domain> and its content host), pointing at their own
// server: the claim, release and listing workflows, the hourly sweep that
// removes expired names and their DNS records, the event_domains SQL, and the
// /v1/events routes. It runs on the Sites core (host/sites) and its eventdns
// provider; nothing but composition imports it.
package events

import "github.com/vsriram/simple-host/host/sites"

// Service is the Sites core service the event-name workflows run on.
type Service struct{ *sites.Service }
