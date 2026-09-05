// Fixture tests for web/analytics-parse.js.  node web/analytics-parse.test.js
//
// Fixtures, not live data: live traffic never produces the shapes that matter
// (an obsolete response, a truncated body, a site with no traffic at all), so
// those are the only cases that can catch the next shape change.
const assert = require('assert');
const fs = require('fs');
const path = require('path');

const src = fs.readFileSync(path.join(__dirname, 'analytics-parse.js'), 'utf8');
const { parseSiteAnalytics, parseAnalyticsSummary, parseSiteGeo } = new Function(
  src + '\nreturn { parseSiteAnalytics: parseSiteAnalytics, parseAnalyticsSummary: parseAnalyticsSummary, parseSiteGeo: parseSiteGeo };'
)();

let n = 0, failed = 0;
function test(name, fn) {
  n++;
  try {
    fn();
    console.log('ok ' + n + ' - ' + name);
  } catch (e) {
    failed++;
    console.log('not ok ' + n + ' - ' + name + '\n    ' + (e && e.message));
  }
}

const zero = [0, 0];
const counts = (views, visitors) => ({ views, visitors });
const split = (p, b, i, u) => ({
  person: counts(p[0], p[1]), bot: counts(b[0], b[1]),
  infra: counts(i[0], i[1]), unknown: counts(u[0], u[1]),
});
const day = (d, s) => Object.assign({ day: d }, s);
const hour = (h, s) => Object.assign({ hour: h }, s);
const site = (name, s) => Object.assign({ name }, s);

// ── 1. the current four-class shape ────────────────────────────────────────
const fourClass = {
  range_days: 30,
  classified_from: '2026-08-22',
  totals: split([306, 258], [1841, 92], [3612, 2], [44, 31]),
  last_24h: split([12, 9], [63, 7], [120, 1], zero),
  daily: [
    day('2026-08-20', split(zero, zero, zero, [21, 15])),
    day('2026-08-21', split(zero, zero, zero, [23, 16])),
    day('2026-08-22', split([18, 14], [77, 5], [120, 1], zero)),
    day('2026-08-31', split([27, 21], [95, 6], [120, 1], zero)),
    day('2026-09-01', split([12, 9], [63, 7], [120, 1], zero)),
  ],
  hourly: [
    hour('2026-09-01T03:00:00Z', split([4, 3], [11, 2], [5, 1], zero)),
    hour('2026-09-01T04:00:00Z', split([8, 6], [52, 5], [5, 1], zero)),
  ],
};

test('four-class site analytics parses', () => {
  const a = parseSiteAnalytics(fourClass);
  assert.strictEqual(a.rangeDays, 30);
  assert.strictEqual(a.classifiedFrom, '2026-08-22');
  assert.strictEqual(a.totals.person.views, 306);
  assert.strictEqual(a.totals.person.visitors, 258);
  assert.strictEqual(a.totals.bot.views, 1841);
  assert.strictEqual(a.totals.infra.views, 3612);
  assert.strictEqual(a.totals.unknown.views, 44);
  assert.strictEqual(a.last24h.person.views, 12);
  assert.strictEqual(a.daily.length, 5);
  assert.strictEqual(a.daily[0].day, '2026-08-20');
  assert.strictEqual(a.daily[0].unknown.views, 21);
  assert.strictEqual(a.daily[4].person.views, 12);
  assert.strictEqual(a.hourly[1].hour, '2026-09-01T04:00:00Z');
  assert.strictEqual(a.hourly[1].person.views, 8);
  // What each page actually renders: people-only sums off daily[].
  assert.strictEqual(a.daily.reduce((t, d) => t + d.person.views, 0), 57);
  assert.strictEqual(a.daily.reduce((t, d) => t + d.unknown.views, 0), 44);
});

test('four-class analytics summary parses and keeps server order', () => {
  const s = parseAnalyticsSummary({
    range_days: 30,
    sites: [
      site('ielts', split([306, 258], [1841, 92], [3612, 2], [44, 31])),
      site('vineetsriram', split([41, 33], [612, 44], [3610, 2], zero)),
    ],
  });
  assert.strictEqual(s.rangeDays, 30);
  assert.deepStrictEqual(s.sites.map(x => x.name), ['ielts', 'vineetsriram']);
  assert.strictEqual(s.sites[0].person.views, 306);
  assert.strictEqual(s.sites[1].bot.views, 612);
});

// ── 2. the obsolete flat shape — the regression test for the shipped bug ────
test('obsolete flat site analytics throws instead of rendering zeros', () => {
  const flat = {
    range_days: 30,
    totals: { views: 306, visitors: 258 },
    daily: [{ day: '2026-09-01', views: 12, visitors: 9 }],
  };
  assert.throws(() => parseSiteAnalytics(flat), /totals\.person/);
});

test('obsolete flat analytics summary throws instead of rendering zeros', () => {
  const flat = { range_days: 30, sites: [{ name: 'ielts', views: 306, visitors: 258 }] };
  assert.throws(() => parseAnalyticsSummary(flat), /sites\[0\]\.person/);
});

// ── 3. a site with zero traffic ────────────────────────────────────────────
test('zero traffic parses, and every number is a real zero', () => {
  const none = split(zero, zero, zero, zero);
  const a = parseSiteAnalytics({
    range_days: 7,
    classified_from: '2026-08-22',
    totals: none,
    last_24h: none,
    daily: ['2026-08-26', '2026-08-27', '2026-08-28', '2026-08-29', '2026-08-30', '2026-08-31', '2026-09-01']
      .map(d => day(d, none)),
    hourly: [hour('2026-09-01T04:00:00Z', none)],
  });
  assert.strictEqual(a.rangeDays, 7);
  assert.strictEqual(a.totals.person.views, 0);
  assert.strictEqual(a.totals.person.visitors, 0);
  assert.strictEqual(a.daily.length, 7);
  assert.strictEqual(a.daily.reduce((t, d) => t + d.person.views, 0), 0);
});

// ── 4. pre-classifier history ──────────────────────────────────────────────
test('unknown-only history parses, with classified_from absent', () => {
  const a = parseSiteAnalytics({
    range_days: 30,
    totals: split(zero, zero, zero, [512, 340]),
    last_24h: split(zero, zero, zero, zero),
    daily: [
      day('2026-07-30', split(zero, zero, zero, [260, 171])),
      day('2026-07-31', split(zero, zero, zero, [252, 169])),
    ],
    hourly: [hour('2026-09-01T04:00:00Z', split(zero, zero, zero, zero))],
  });
  assert.strictEqual(a.classifiedFrom, '');
  assert.strictEqual(a.totals.unknown.views, 512);
  assert.strictEqual(a.totals.person.views, 0);
  assert.strictEqual(a.daily.reduce((t, d) => t + d.unknown.views, 0), 512);
});

// ── 5. malformed / truncated payloads ──────────────────────────────────────
test('malformed and truncated payloads throw, naming the field', () => {
  const ok = split([1, 1], [1, 1], [1, 1], zero);
  const cases = [
    [null, /expected an object/],
    ['{"range_days":30', /expected an object/],
    [{}, /range_days/],
    [{ range_days: 30 }, /totals/],
    // truncated mid-totals: the classes after the cut are simply gone
    [{ range_days: 30, totals: { person: counts(5, 3), bot: counts(2, 1) } }, /totals\.infra/],
    [{ range_days: 30, totals: ok, last_24h: ok, daily: null }, /daily: expected an array/],
    [{
      range_days: 30, totals: ok, last_24h: ok, hourly: [],
      daily: [day('2026-09-01', ok), { day: '2026-09-02', person: counts(1, 1) }],
    }, /daily\[1\]\.bot/],
    [{
      range_days: 30, totals: ok, last_24h: ok, daily: [],
      hourly: [Object.assign({}, ok)],
    }, /hourly\[0\]\.hour/],
    [{ range_days: 30, totals: split(['306', 258], zero, zero, zero) }, /totals\.person\.views/],
    [{ range_days: 30, totals: split([-1, 0], zero, zero, zero) }, /totals\.person\.views/],
    [{ range_days: 30, totals: split([1.5, 0], zero, zero, zero) }, /totals\.person\.views/],
    [{ range_days: 30, totals: ok, last_24h: ok, daily: [], hourly: [], classified_from: 20260822 }, /classified_from/],
  ];
  for (const [payload, want] of cases) {
    assert.throws(() => parseSiteAnalytics(payload), want, 'payload: ' + JSON.stringify(payload));
  }
  assert.throws(() => parseAnalyticsSummary({ range_days: 30 }), /sites: expected an array/);
  assert.throws(() => parseAnalyticsSummary({ range_days: 30, sites: [{}] }), /sites\[0\]\.name/);
});

// ── 6. the country breakdown ───────────────────────────────────────────────
const country = (code, name, s) => Object.assign({ country: code, country_name: name }, s);

test('country breakdown parses and keeps server order', () => {
  const g = parseSiteGeo({
    range_days: 30,
    countries: [
      country('US', 'United States', split([120, 45], [610, 31], [3610, 2], zero)),
      country('IN', 'India', split([64, 29], [88, 9], zero, zero)),
      country('XX', 'Unknown', split([9, 6], [140, 12], zero, [44, 31])),
    ],
  });
  assert.strictEqual(g.rangeDays, 30);
  assert.deepStrictEqual(g.countries.map(c => c.country), ['US', 'IN', 'XX']);
  assert.strictEqual(g.countries[0].countryName, 'United States');
  assert.strictEqual(g.countries[0].person.views, 120);
  assert.strictEqual(g.countries[1].person.visitors, 29);
  assert.strictEqual(g.countries[2].countryName, 'Unknown');
  assert.strictEqual(g.countries[2].unknown.views, 44);
  // The share denominator the page divides by.
  assert.strictEqual(g.countries.reduce((t, c) => t + c.person.views, 0), 193);
});

test('a site with no geography yet parses as an empty list, not an error', () => {
  const g = parseSiteGeo({ range_days: 7, countries: [] });
  assert.strictEqual(g.rangeDays, 7);
  assert.deepStrictEqual(g.countries, []);
});

test('malformed geography throws, naming the field', () => {
  const ok = split([1, 1], [1, 1], [1, 1], zero);
  const cases = [
    [null, /site geo: expected an object/],
    [{ range_days: 30 }, /countries: expected an array/],
    [{ countries: [] }, /range_days/],
    [{ range_days: 30, countries: [{}] }, /countries\[0\]\.country:/],
    // a code with no display name — would render as a nameless row
    [{ range_days: 30, countries: [Object.assign({ country: 'US' }, ok)] }, /countries\[0\]\.country_name/],
    // the flat pre-four-class shape: counts, no person/bot/infra split
    [{ range_days: 30, countries: [{ country: 'US', country_name: 'United States', views: 120, visitors: 45 }] },
      /countries\[0\]\.person/],
    [{ range_days: 30, countries: [country('US', 'United States', ok), country('IN', 'India', { person: counts(1, 1) })] },
      /countries\[1\]\.bot/],
    [{ range_days: 30, countries: [country('US', 'United States', split(['120', 45], zero, zero, zero))] },
      /countries\[0\]\.person\.views/],
  ];
  for (const [payload, want] of cases) {
    assert.throws(() => parseSiteGeo(payload), want, 'payload: ' + JSON.stringify(payload));
  }
});

console.log('\n' + (n - failed) + '/' + n + ' passed');
process.exit(failed ? 1 : 0);
