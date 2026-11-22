// Package accountdata is a person's own data: the download of everything
// held about them (GET /v1/me/export.zip) and the erasure of an account,
// asked by the person (DELETE /v1/me) or the operator
// (DELETE /v1/admin/users/{id}). The erasure itself spans the modules, so it
// is host/ops's Cutoff.ErasePerson; this package reads the request, checks it
// came with the person's own key, and answers.
package accountdata
