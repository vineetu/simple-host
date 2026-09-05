// Strict parser for the analytics endpoints, shared by index.html,
// showcase.html and analytics.html. It is INLINED into those pages by
// scripts/inline-analytics-parser.sh: showcase.html is served from the apex and
// from the content host, and a <script src="/..."> resolves on one and 404s on
// the other.
//
// It throws instead of defaulting. Defaulting a missing field to 0 is how the
// four-class response shipped against a page that still read the flat one and
// rendered "0 views / 0 visitors" for every site without a single error.

function anInt(v, path) {
  if (typeof v !== 'number' || !Number.isInteger(v) || v < 0) {
    throw new Error(path + ': expected a non-negative integer, got ' + JSON.stringify(v));
  }
  return v;
}

function anCounts(v, path) {
  if (!v || typeof v !== 'object') throw new Error(path + ': expected {views, visitors}');
  return { views: anInt(v.views, path + '.views'), visitors: anInt(v.visitors, path + '.visitors') };
}

function anSplit(v, path) {
  if (!v || typeof v !== 'object') throw new Error(path + ': expected a person/bot/infra/unknown split');
  return {
    person: anCounts(v.person, path + '.person'),
    bot: anCounts(v.bot, path + '.bot'),
    infra: anCounts(v.infra, path + '.infra'),
    unknown: anCounts(v.unknown, path + '.unknown'),
  };
}

// daily[] and hourly[] carry their split inline alongside a day/hour key.
function anBuckets(v, path, key) {
  if (!Array.isArray(v)) throw new Error(path + ': expected an array');
  return v.map(function (b, i) {
    var at = path + '[' + i + ']';
    if (!b || typeof b[key] !== 'string' || !b[key]) {
      throw new Error(at + '.' + key + ': expected a non-empty string');
    }
    var out = anSplit(b, at);
    out[key] = b[key];
    return out;
  });
}

// GET /v1/sites/{sitename}/analytics
function parseSiteAnalytics(data) {
  if (!data || typeof data !== 'object') throw new Error('site analytics: expected an object');
  if (data.classified_from !== undefined && typeof data.classified_from !== 'string') {
    throw new Error('site analytics.classified_from: expected a string');
  }
  return {
    rangeDays: anInt(data.range_days, 'site analytics.range_days'),
    // omitempty on the wire: absent means nothing has been classified yet.
    classifiedFrom: data.classified_from || '',
    totals: anSplit(data.totals, 'site analytics.totals'),
    last24h: anSplit(data.last_24h, 'site analytics.last_24h'),
    daily: anBuckets(data.daily, 'site analytics.daily', 'day'),
    hourly: anBuckets(data.hourly, 'site analytics.hourly', 'hour'),
  };
}

// GET /v1/sites/{sitename}/analytics/geo
//
// country is the ISO-3166 code, "XX" for an IP that resolved to nothing;
// country_name is the display name, "Unknown" for XX. Both are required: a
// blank name would render as a nameless row that reads like a rendering bug,
// and defaulting it to the code would quietly invent a country called "XX".
function parseSiteGeo(data) {
  if (!data || typeof data !== 'object') throw new Error('site geo: expected an object');
  if (!Array.isArray(data.countries)) throw new Error('site geo.countries: expected an array');
  return {
    rangeDays: anInt(data.range_days, 'site geo.range_days'),
    countries: data.countries.map(function (c, i) {
      var at = 'site geo.countries[' + i + ']';
      if (!c || typeof c.country !== 'string' || !c.country) {
        throw new Error(at + '.country: expected a non-empty string');
      }
      if (typeof c.country_name !== 'string' || !c.country_name) {
        throw new Error(at + '.country_name: expected a non-empty string');
      }
      var out = anSplit(c, at);
      out.country = c.country;
      out.countryName = c.country_name;
      return out;
    }),
  };
}

// GET /v1/analytics/sites
function parseAnalyticsSummary(data) {
  if (!data || typeof data !== 'object') throw new Error('analytics summary: expected an object');
  if (!Array.isArray(data.sites)) throw new Error('analytics summary.sites: expected an array');
  return {
    rangeDays: anInt(data.range_days, 'analytics summary.range_days'),
    sites: data.sites.map(function (s, i) {
      var at = 'analytics summary.sites[' + i + ']';
      if (!s || typeof s.name !== 'string' || !s.name) {
        throw new Error(at + '.name: expected a non-empty string');
      }
      var out = anSplit(s, at);
      out.name = s.name;
      return out;
    }),
  };
}
