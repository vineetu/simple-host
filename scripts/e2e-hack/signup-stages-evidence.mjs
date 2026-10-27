// Screenshots for signup-stages findings whose steps had no page attached in the full run.
// Uses the co-organiser account of set 3 as the event owner (organiser hit the 3/day cap).
import { Run, accounts, stamp, goto, api, deleteEvent } from './lib.mjs';
const OUT = '/tmp/claude-1001/-home-ubuntu-workspace-4/2268cadb-dfb1-4db4-9617-f1516b000a56/scratchpad/e2e-prod';
const A = accounts(3); const OWN = A['co-organiser'][0]; const P = A.participant;
const slug = 'e2e-signup-' + stamp + 'x';
const run = new Run('signup-stages-evidence', OUT);
const sleep = ms => new Promise(r => setTimeout(r, ms));
let made = false;
try {
  const b = await run.browser('chromium');
  await run.step('SIM-235', 'setup (REST): event, approval on, one question, open; p2 applies and is rejected', null, async t => {
    const c = await api(OWN.api_key, 'POST', '/v1/hack/events', { title: 'E2E evidence ' + stamp, slug, organiser_name: 'E2E', organisation: 'e2e', contact_email: 'e2e@example.com', purpose: 'Automated test. Safe to delete.', starts_at: new Date(Date.now() + 7 * 864e5).toISOString().slice(0, 10), expected_participants: 10, time_zone: 'UTC' });
    if (c.status >= 300) { t.fail('create ' + c.status + JSON.stringify(c.data)); return; }
    made = true;
    await api(OWN.api_key, 'PUT', `/v1/hack/events/${slug}/registration`, { questions: [{ id: 'q1', prompt: 'Idea', required: false }], approval_required: true });
    await api(OWN.api_key, 'POST', `/v1/hack/events/${slug}/stage`, { stage: 'open' });
  });
  const ev = await api(OWN.api_key, 'GET', `/v1/hack/events/${slug}`);
  const joinUrl = ev.data.organiser.join_url; const code = joinUrl.split('/').pop();
  const ap = await api(P[1].api_key, 'POST', `/v1/hack/join/${code}`, { accept_coc: true, display_name: 'Rejected Person', answers: { q1: 'x' } });
  const apps = await api(OWN.api_key, 'GET', `/v1/hack/events/${slug}/applications`);
  const uid = (apps.data || []).find(a => a.display_name === 'Rejected Person')?.user_id;
  await api(OWN.api_key, 'POST', `/v1/hack/events/${slug}/applications/${uid}/decision`, { decision: 'rejected' });
  const p2 = await run.person(b, P[1], { label: 'p2' });
  await run.step('SIM-235', 'rejected person opens the join link: "You\'re already in" (evidence)', p2.page, async t => {
    await goto(p2.page, joinUrl); await sleep(1500);
    t.fail('evidence: ' + (await p2.page.locator('#view-join').innerText()).replace(/\s+/g, ' ').slice(0, 200));
  });
  await api(OWN.api_key, 'POST', `/v1/hack/events/${slug}/stage`, { stage: 'closed' });
  const p16 = await run.person(b, P[15], { label: 'p16' });
  await run.step('SIM-238', 'join link at Entries closed says "This event is over." (evidence)', p16.page, async t => {
    await goto(p16.page, joinUrl); await sleep(1500);
    t.fail('evidence: ' + (await p16.page.locator('#view-join').innerText()).replace(/\s+/g, ' ').slice(0, 200));
  });
  await api(OWN.api_key, 'POST', `/v1/hack/events/${slug}/stage`, { stage: 'results' });
  const v = await run.person(b, null, { label: 'visitor' });
  await run.step('SIM-245', 'public page at Results with nothing published says "Results are in." (evidence)', v.page, async t => {
    await v.page.goto(`https://${slug}.simple-hack.app/`); await sleep(1000);
    t.fail('evidence: ' + (await v.page.locator('.hero').innerText()).replace(/\s+/g, ' ').slice(0, 200));
  });
} finally {
  if (made) console.log('CLEANUP ' + slug + ' DELETE ' + await deleteEvent(OWN.api_key, slug) + ' GET ' + (await api(OWN.api_key, 'GET', `/v1/hack/events/${slug}`)).status);
  await run.done();
}
