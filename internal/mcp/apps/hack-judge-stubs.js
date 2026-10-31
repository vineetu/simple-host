// Hand-written. Stands in for internal/handler/static/hack/nav.js and
// tour.js, which the bundled judge screens (hack-judge-bundle.js) import
// but which this frame answers differently: an in-frame router instead of
// real page URLs (no sidebar, no browser address bar to navigate), and no
// guided walkthrough (the tour is a first-visit, full-page feature; this
// frame is one small view at a time). Loaded before the bundle, since the
// bundle's module closures read these globals as soon as they run.
(function () {
  'use strict';
  // nav.js's href(role, slug, section) returns a real page URL
  // (/e/<slug>/<role>?s=<section>); every judge screen call here passes
  // role 'judge' for the one event already open in this frame, so only
  // the section matters. hack-judge-view.js's click handler intercepts
  // '#v=' links and switches views without leaving the frame. The
  // section names are the website's own (home, queue, scoring, rubric);
  // hack-judge-view.js's VIEW_ALIAS maps them onto this frame's four
  // views (invite, queue, score, rubric), so that alias lives there, not
  // duplicated here.
  window.__hackHref = function (role, slug, section) { return '#v=' + section; };
  // tour.js's startTour(name, ctx, opts) runs a guided walkthrough;
  // judge-scoring.js calls it on first open and from a Help deep link.
  // No-op here.
  window.__hackStartTour = function () {};
})();
