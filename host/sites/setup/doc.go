// Package setup is the first-run setup of a self-hosted instance: the
// password-gated /v1/setup routes that choose the instance's own domain or a
// free name, check its DNS and write the saved setup (host/ops). host/server
// mounts it while the instance has no SITE_DOMAIN. It imports the Sites core,
// eventdns and host/ops, never another Sites feature.
package setup
