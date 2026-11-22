// Package sitesroute is what the Sites routes share: Handler (the instance's
// site services, locks, limiters, caches and collaborators, and the start-up
// setters), the site response view, the error writers, the admin by-id
// wrapper (AsSiteOwner) and the owner-browser mark. A feature package mounts
// its routes on it (Register(mux, h *sitesroute.Handler, ...)); siteshttp
// embeds it. It imports the modules' cores and host/web, never a Sites
// feature or a routes package.
package sitesroute
