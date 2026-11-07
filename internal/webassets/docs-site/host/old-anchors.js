// Permanent compatibility for fragments retained by the /docs.html redirect.
(() => {
  const targets = {"add-photos": "/docs/guides/dashboard/#add-photos", "address-families": "/docs/guides/addresses/", "connect-a-domain": "/docs/guides/addresses/#connect-your-own-domain", "duplicates-retries-and-suspension": "/docs/webhooks/#retries-and-suspension", "go": "/docs/webhooks/#verify-before-processing", "headers-and-body": "/docs/webhooks/#headers-and-body", "install-skills": "/install.html", "mcp-calls": "/docs/guides/notifications/#subscribe-with-mcp-events", "node": "/docs/webhooks/#verify-before-processing", "notifications": "/docs/guides/notifications/", "python": "/docs/webhooks/#verify-before-processing", "receiver-setup": "/docs/guides/notifications/#get-a-webhook-when-a-visitor-writes", "storage-details": "/docs/guides/storage/#write-to-storage-from-your-own-code", "swagger-ui": "/docs/api/", "verify-before-processing": "/docs/webhooks/#verify-before-processing"};
  const redirect = () => {
    const target = targets[location.hash.slice(1)];
    if (target) location.replace(target);
  };
  // A cached 301 can land in the already-open docs home without reloading it.
  addEventListener('hashchange', redirect);
  redirect();
})();
