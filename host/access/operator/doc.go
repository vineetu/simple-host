// Package operator is the operator's account routes: making accounts in bulk
// (POST /v1/admin/users), reissuing an account's key, and the sign-ups
// report by source (GET /v1/admin/signups and .csv, signups.go). Every route
// is the admin's alone (auth.Admin).
package operator
