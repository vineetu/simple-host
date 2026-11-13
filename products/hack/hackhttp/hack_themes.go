package hackhttp

// hackThemes is the Look tab's set of whole-page styles for the public event
// page (SIM-353, 0011-hack-event-theme.sql). "" (Classic) is not listed here:
// it is always valid and means today's look, unchanged. The CSS for each of
// these lives in internal/webassets/static/hack/themes.css, scoped under
// .pub-event[data-theme="<id>"]; this file is the one place that says which
// ids are real.
var hackThemes = []string{"poster", "confetti", "boardroom", "garden", "terminal"}

// hackThemeValid reports whether choice is "" or one of hackThemes.
func hackThemeValid(choice string) bool {
	if choice == "" {
		return true
	}
	for _, t := range hackThemes {
		if t == choice {
			return true
		}
	}
	return false
}
