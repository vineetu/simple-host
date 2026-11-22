// Package editors is site editors: a person who may publish, update and
// roll back one site of an account (a person's or a team's) without
// belonging to it. The account, or a member of the team with owner=<team>,
// lists, adds and removes them (routes.go); whether an editor may act is
// access.ActsAsOwner alone, and the site_editors table stays with the core's
// repo because that check and Sites read it.
package editors
