// Package profile is the person's account as they see and change it: GET and
// PATCH /v1/me (me.go, patch.go), the address check (handles.go), changing
// the sign-in email with its undo link, and the linked sign-ins (email.go).
// A new address is judged by the core (access.JudgeHandle) and renamed with
// the account's sites by Sites (sites.Service.UpdateProfile).
package profile
