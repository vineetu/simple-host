// Package members owns Hack membership and registration workflows.
//
// Who sees sign-up answers (and an applicant's city, country, age range and
// gender): organisers only, through Applications: GET /applications,
// hack_get_applications. The participants CSV (export/participants.csv,
// hack_export_participants) adds city, country, age range and gender, not
// the answers. Analytics never gets them.
package members
