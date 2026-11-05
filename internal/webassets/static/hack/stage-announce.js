// Pure module, no DOM use: the suggested title/body for the "Tell
// participants?" dialog organiser-overview.js offers after a stage move
// (or Publish results) succeeds (SIM-341, owner: "Every stage change
// should offer to send an announcement, with the option not to send, or
// to edit what is sent."). Reused by the website, the organiser MCP App
// bundle (bundlegen pulls in every real import, this one included) and
// scripts/tests/hack-stage-announce.test.mjs.
//
// `move` is a target stage id (draft, open, building, closed, judging,
// results, archived) or the synthetic 'publish_results' (Publish results
// is its own server call, not a stage). `opts.back` is true for a
// backward move picked through Change stage (an earlier stage than the
// event's current one) -- draft, open, building, closed or judging only:
// the stage picker never offers archived as a target (internal/
// webassets/static/hack/screens/_orgA-util.js stagePicker), and nothing
// sits after results except archived, so results is never a backward
// target either. `opts.eventTitle` is accepted for a future
// personalised line but unused today -- the owner-approved wording below
// is fixed, plain text.
//
// Every string here must fit the announcements endpoint's real limits:
// title 1-120 chars, body 1-3000 chars (internal/hackhttp/hack_content.go
// postAnnouncement's checkHackLine(title, 1, 120) / checkHackText(body, 1,
// 3000)). All suggestions here are well under both.
export function suggestedAnnouncement(move, opts) {
  const back = !!(opts && opts.back)
  if (move === 'publish_results') {
    return { title: 'Results are out', body: 'Results are out. See them on the event page.' }
  }
  if (back) {
    switch (move) {
      case 'draft':
        return { title: 'Sign-up is paused', body: 'Sign-up is paused for now. Everyone who joined stays in.' }
      case 'open':
      case 'building':
        return { title: 'Entries are open again', body: 'Entries are open again. You can edit your entry and website.' }
      case 'closed':
        return { title: 'Entries are closed', body: 'Entries are closed and judges are starting to score. Thanks for building!' }
      case 'judging':
        return { title: 'Judging has reopened', body: 'Judging has reopened. Entries stay as they are.' }
    }
    return null
  }
  switch (move) {
    case 'open':
      return { title: 'Sign-up is open', body: 'Sign-up is open. Join with the event link and find a team.' }
    case 'building':
      return { title: 'Building has started', body: 'Building has started. Add your entry and website before entries close.' }
    case 'closed':
      return { title: 'Entries are closed', body: 'Entries are closed and judges are starting to score. Thanks for building!' }
    case 'judging':
      return { title: 'Judging has started', body: 'Judging has started. Entries, teams and websites can no longer be changed.' }
    case 'results':
      return { title: 'Judging is done', body: 'Judging is done. Results are coming soon.' }
    case 'archived':
      return { title: 'The event has ended', body: 'The event has ended. Thanks for taking part. The event page stays up as a record.' }
  }
  return null
}
