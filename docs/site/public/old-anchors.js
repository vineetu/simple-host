// Permanent compatibility for fragments retained by the /docs.html redirect.
(() => {
  const targets = @TARGETS@;
  const redirect = () => {
    const target = targets[location.hash.slice(1)];
    if (target) location.replace(target);
  };
  // A cached 301 can land in the already-open docs home without reloading it.
  addEventListener('hashchange', redirect);
  redirect();
})();
