// Package lifecycle is a site's life after its first deploy: rename,
// visibility and allowed origins, offline and keep settings, and Recently
// deleted (restore, and the purge of sites whose restore window has passed).
// Its Service embeds the core *sites.Service; deleting a site (sites.Delete,
// Trash) stays in the core because archives, address moves and the idle
// cleanup also delete. Its routes (routes.go, Register) include DELETE, PATCH
// (rename, offline), restore and Recently deleted; the hourly purge worker
// stays in siteshttp with the other site workers. It imports the Sites core,
// repo and sitesroute, never another feature or a route package.
package lifecycle
