// The public pages' header for someone signed in: Account and Sign out next
// to "Your sites". Signed out, the header stays as the page wrote it.
(function () {
  var link = document.getElementById("dash-link");
  if (!link) return;
  fetch("/v1/me", { credentials: "same-origin" }).then(function (r) {
    if (!r.ok) return;
    var nav = document.createElement("nav");
    nav.className = "sh-nav";
    nav.setAttribute("aria-label", "Your account");
    var account = document.createElement("a");
    account.href = "/dashboard/account";
    account.textContent = "Account";
    var out = document.createElement("a");
    out.href = "/";
    out.textContent = "Sign out";
    out.addEventListener("click", function (e) {
      e.preventDefault();
      fetch("/v1/auth/signout", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json", "X-SH-CSRF": "1" }, body: "{}" })
        .then(function () { location.assign("/"); });
    });
    nav.appendChild(account);
    nav.appendChild(out);
    link.after(nav);
    link.closest(".sh-bar").classList.add("sh-signed");
  }).catch(function () {});
})();
