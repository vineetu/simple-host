package siteshttp

// One light/dark setting for the whole site (INTENT, 2026-09-28): every page
// follows the visitor's system until they pick Light or Dark in the header's
// theme menu, and that one choice (localStorage 'sh-theme') applies to every
// page. The only code that reads the system setting or the stored choice is
// partials/theme.html; no page has theme logic or an auto-dark stylesheet of
// its own.

// themeScriptNeedle marks the shared theme script in a served page.
const themeScriptNeedle = "window.shTheme={"
