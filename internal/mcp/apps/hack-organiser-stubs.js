// Hand-written. Stands in for internal/handler/static/hack/nav.js and
// tour.js, which the bundled organiser screens (hack-organiser-bundle.js)
// import but which this frame answers differently: an in-frame router for
// the three views it actually has (overview, people, judging), real
// website links for everything else nav.js's organiser section lists
// (event-page, announcements, teams, entries, results, settings,
// assignments -- later phases), and no guided walkthrough.
//
// __ORIGIN__ is substituted by hack_apps.go's readHackApp before this file
// reaches the page (the one placeholder this hand-written file carries;
// everything else here runs as committed).
(function () {
  'use strict';
  window.__hackOrgOrigin = '__ORIGIN__';
  // The three sections this frame actually renders in-frame, keyed by the
  // real website's own section names (nav.js's organiser list uses
  // 'overview', 'people', 'judging' already -- no alias needed here,
  // unlike the judge frame's home/queue/scoring renaming).
  var IN_FRAME = { overview: 1, people: 1, judging: 1 };
  // nav.js's href(role, slug, section) returns a real page URL
  // (/e/<slug>/manage?s=<section>) for the full organiser app; every
  // organiser screen call here passes role 'organiser' for the one event
  // already open in this frame. A section this frame can show switches
  // in-frame ('#v=<section>', intercepted by hack-organiser-view.js's
  // click handler); anything else (teams, entries, results, event page,
  // settings, the assignment grid -- none built yet) opens the real
  // website instead, through ui/open-link, same as a link to an unrelated
  // host.
  window.__hackHref = function (role, slug, section) {
    if (role === 'organiser' && IN_FRAME[section || 'overview']) return '#v=' + (section || 'overview');
    return window.__hackOrgOrigin + '/e/' + encodeURIComponent(slug) + '/manage' + (section ? '?s=' + encodeURIComponent(section) : '');
  };
  // tour.js's startTour(name, ctx, opts) runs a guided walkthrough;
  // organiser-overview.js calls it on first open and from a Help deep
  // link. No-op here, same as the judge frame.
  window.__hackStartTour = function () {};
})();
