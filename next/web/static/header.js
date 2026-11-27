// Who is looking at a public page. It sets data-account="in" or "out" on
// <html>; the page's own markup says what each sees (data-when="signed-in" or
// "signed-out", shown by the style sheet), and a link marked data-sign-out
// signs the person out.
(function () {
  var root = document.documentElement;
  function set(state) { root.dataset.account = state; }
  fetch("/v1/me", { credentials: "same-origin" })
    .then(function (r) { set(r.ok ? "in" : "out"); })
    .catch(function () { set("out"); });
  document.addEventListener("click", function (e) {
    var a = e.target.closest && e.target.closest("[data-sign-out]");
    if (!a) return;
    e.preventDefault();
    fetch("/v1/auth/signout", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json", "X-SH-CSRF": "1" }, body: "{}" })
      .then(function (r) { if (r.ok) location.assign("/"); });
  });
})();
