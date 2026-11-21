// Package entries owns Hack entries and judging workflows; it never writes HTTP responses.
//
// Who sees per-judge scores and comments (all read through scoreRows in
// repository/hackentries, so a field added there reaches each of them):
//   - the judge, for their own scores only: GET /judge/scores/{team}, hack_get_judge_scores
//   - organisers, every judge by name: GET /export/scores.csv, hack_export_scores
//   - the team, comments only with no judge named, once results are published:
//     GET /my-results, hack_get_my_results (TeamComments)
//   - everyone else sees totals only: GET /results, hack_get_public_results, results.csv
//
// Who sees entries: the team (GET /entry, hack_get_entry); organisers and
// judges (GET /entries, hack_get_entries, the judge queue); organisers'
// exports (entries.csv, projects.tar.gz); participants once judging starts
// (GET /submissions); the public only for teams shown in published results.
package entries
