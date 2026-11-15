package mcp

// Contribution is everything a product adds to the tool server. The zero value adds nothing.
type Contribution struct {
	Families      []FamilyTools
	Notifications []Tool
	SiteVariants  map[string]func(Tool, Limits) Tool
	Instructions  func(mode string) string
	Rewrite       func(text, apiHost string) string
	StorageError  func(message string) string
	NoTarget      func(tool string, listable bool) (text string, structured map[string]any, isError bool)
	Apps          func(AppContext) AppContribution
}

// AppContext contains server facts and shared metadata, with no request data.
type AppContext struct {
	APIHost string
	Fixed   bool
	Meta    func(AppScreen) map[string]any
}

// AppContribution supplies one server's product screens.
type AppContribution struct {
	Screens   func(Caller) []AppScreen
	Resources func(Caller, bool) []map[string]any
	Gate      func(Caller, bool) bool
	Read      func(Request, Caller, bool) (Response, bool)
	AppFamily func(base []Tool) FamilyTools
}

// FamilyTools groups declarations in listing order.
type FamilyTools = familyTools

func (s *Server) productScreens(c Caller) []AppScreen {
	if screens := s.apps.Screens; screens != nil {
		return screens(c)
	}
	return nil
}
func (s *Server) productAppsGate(c Caller, ui bool) bool {
	return s.apps.Gate != nil && s.apps.Gate(c, ui)
}
func (s *Server) productAppResources(c Caller, allowed bool) []any {
	var out []any
	if resources := s.apps.Resources; resources != nil {
		for _, resource := range resources(c, allowed) {
			out = append(out, resource)
		}
	}
	return out
}
