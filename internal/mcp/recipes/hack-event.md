Build the organiser's event website from this complete index.html and publish with hack_publish_event_website.
Restyle freely, but keep the live fetch; never hard-code stage, dates, announcements, gallery or results.

```html
<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Event</title>
<style>
body{font:17px/1.6 system-ui,sans-serif;max-width:800px;margin:40px auto;padding:0 20px;color:#202020;background:#faf9f6}h1,h2,h3{line-height:1.2}section{margin:32px 0}article{border-top:1px solid #ccc;padding:16px 0}a{color:#176354}main>a{display:inline-block;padding:8px 20px;background:#176354;color:white;border-radius:4px}img{display:block;max-width:100%;height:auto}p{white-space:pre-wrap}
</style>
<main id="event"></main>
<script>
const feed = 'https://simple-hack.app/v1/hack/events/{event}/public';
const root = document.querySelector('#event');
let inFlight = false;
function add(parent, tag, text) {
  const node = document.createElement(tag);
  if (text != null) node.textContent = text;
  parent.append(node);
  return node;
}
function httpURL(value) {
  try {
    const url = new URL(value);
    return ['http:', 'https:'].includes(url.protocol) ? url.href : null;
  } catch { return null; }
}
function link(parent, title, value) {
  const url = httpURL(value);
  if (url) add(parent, 'a', title).href = url;
}
function section(parent, title) {
  const node = add(parent, 'section');
  add(node, 'h2', title);
  return node;
}
function draw(event) {
  const page = document.createDocumentFragment();
  const date = (value, allDay = false) => {
    if (!value) return '';
    const options = {timeZone: event.time_zone || 'UTC', year: 'numeric', month: 'short', day: 'numeric',
      ...(allDay ? {} : {hour: 'numeric', minute: '2-digit', timeZoneName: 'short'})};
    try { return new Date(value).toLocaleString(undefined, options); }
    catch { return new Date(value).toLocaleString(undefined, {...options, timeZone: 'UTC'}); }
  };
  document.title = event.title;
  add(page, 'h1', event.title);
  add(page, 'p', event.tagline);
  const resultsOut = event.results_published && event.results?.length > 0;
  const stage = resultsOut && ['building', 'closed', 'judging'].includes(event.stage) ? 'results' : event.stage;
  const labels = {draft: 'Not open yet', open: 'Sign-up is open', building: 'Teams are building',
    closed: 'Entries are closed', judging: 'Judging is under way',
    results: resultsOut ? 'Results are out' : 'Results coming soon', archived: 'This event has ended'};
  add(page, 'h2', labels[stage]);
  if (['closed', 'judging'].includes(stage)) add(page, 'p', 'Winners will be posted on this page.');
  if (event.apply_url) link(page, 'Apply', event.apply_url);
  else if (['open', 'building'].includes(stage)) add(page, 'p', 'Join with the link from the organisers');
  if (['open', 'building'].includes(stage) && event.submission_deadline) {
    add(page, 'p', new Date(event.submission_deadline) > new Date()
      ? 'Entries close ' + date(event.submission_deadline) : 'Entries are closed');
  }
  const schedule = [...(event.schedule || []), ...(event.fixed_schedule || [])]
    .sort((a, b) => (a.start_at ? new Date(a.start_at).getTime() : Infinity)
      - (b.start_at ? new Date(b.start_at).getTime() : Infinity));
  const next = schedule.find(row => !row.all_day && new Date(row.start_at) > new Date());
  if (next) add(page, 'p', next.title + ' · ' + date(next.start_at));
  if (event.announcements?.length) {
    const area = section(page, 'Announcements');
    for (const row of event.announcements) {
      const item = add(area, 'article');
      add(item, 'h3', row.title);
      add(item, 'p', row.body);
      add(item, 'time', date(row.created_at));
    }
  }
  if (schedule.length) {
    const area = section(page, 'Schedule');
    for (const row of schedule) {
      const item = add(area, 'article');
      add(item, 'h3', row.title);
      add(item, 'p', date(row.start_at, row.all_day) + (row.end_at ? ' – ' + date(row.end_at, row.all_day) : ''));
      add(item, 'p', row.description);
    }
  }
  if (event.gallery?.length) {
    const area = section(page, 'Gallery');
    for (const row of event.gallery) {
      const item = add(area, 'article');
      add(item, 'h3', row.title);
      add(item, 'p', row.tagline);
      link(item, 'Open website', row.url);
      const screenshot = httpURL(row.screenshot_url);
      if (screenshot) {
        const img = add(item, 'img');
        img.src = screenshot;
        img.alt = row.title;
      }
    }
  }
  if (resultsOut) {
    const rows = event.results || [];
    for (const [title, winners] of (event.full_ranking ? [['Ranking', rows]] : [
      ['Overall winners', rows.filter(row => row.overall)],
      ['Track winners', rows.filter(row => row.track_winner)]
    ])) {
      if (!winners.length) continue;
      const area = section(page, title);
      for (const row of winners) {
        const item = add(area, 'article');
        add(item, 'h3', row.team_name);
        if (row.track_name && (event.full_ranking || title === 'Track winners')) add(item, 'p', row.track_name);
        if (event.full_ranking && (row.overall || row.track_winner)) add(item, 'p', 'Winner');
        if (row.rank != null) add(item, 'p', 'Rank: ' + row.rank);
        if (row.score != null) add(item, 'p', 'Score: ' + row.score);
      }
    }
  }
  root.replaceChildren(page);
}
async function refresh() {
  if (inFlight) return;
  inFlight = true;
  try {
    const response = await fetch(feed, {credentials: 'omit', cache: 'no-store'});
    if (!response.ok) return;
    draw(await response.json());
  } catch { /* Keep the last drawn state when the feed is unavailable. */ }
  finally { inFlight = false; }
}
refresh();
setInterval(() => { if (document.visibilityState === 'visible') refresh(); }, 60000);
document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible') refresh(); });
</script>
</html>
```
