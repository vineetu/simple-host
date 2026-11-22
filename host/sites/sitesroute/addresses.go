package sitesroute

// SiteURL is the address handed out for a site: the person address in
// canonical mode, else the path URL on the content host. "" without a handle.
func (h *Handler) SiteURL(handle, name string) string {
	return h.Hosts.Addressing().SiteURL(handle, name)
}

func (h *Handler) PersonPageURL(handle string) string {
	return h.Hosts.Addressing().PersonPageURL(handle)
}
