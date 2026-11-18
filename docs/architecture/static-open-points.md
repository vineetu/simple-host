# Static open points

- Passcode sites require the API on every visit, including visits with a valid unlock cookie. Serving them directly from Static is deferred to later work tracked in Linear.
- When a hostname is claimed after the routing snapshot, Static can forward its root to the API or return 404 for an asset until refresh. The bounded lookup should resolve the domain binding for both paths.
- When a selected home is unavailable, Static can return 404 for a sibling published after the snapshot. It should skip the unavailable home and discover the sibling through the bounded lookup. The switch-on test should also cover a home directory removed after the snapshot when the sibling is already in the copy. Static now redirects that request to the sibling.
- At switch-on, repeat the outage fixtures through the configured front door. With the API down, explicitly public and unlisted sites should retain their content, indexing and cache headers. With only Static disconnected from the database, the healthy API should handle marked pages and preserve both framing headers for a signed-in owner.
