package handler

import "github.com/vsriram/simple-host/internal/db"

// The film's crayon box, with two ballpoint inks. Stored choices are palette names.
var hackCrayons = []struct{ Name, Color string }{
	{"red", "#d15d48"}, {"teal", "#267b79"}, {"indigo", "#4a49a8"},
	{"orange", "#e9a45f"}, {"yellow", "#f2c94c"}, {"grass", "#86ab74"}, {"blush", "#ee8f92"},
}

func hackEventColor(slug, choice string) string {
	for _, c := range hackCrayons {
		if choice == c.Name {
			return c.Color
		}
	}
	// A stable rolling index; this also retains the old fallback icon's slug sum.
	index := 0
	for _, c := range slug {
		index += int(c)
	}
	return hackCrayons[index%len(hackCrayons)].Color
}

func hackColorValid(choice string) bool {
	if choice == "" {
		return true
	}
	for _, c := range hackCrayons {
		if c.Name == choice {
			return true
		}
	}
	return false
}

func hackEventAccent(ev db.Event) string { return hackEventColor(ev.Slug, ev.AccentColor) }

// hackEventInk is the ink colour that reads on a raw --ev background: every
// crayon is dark enough to need the kit's dark ink except teal and indigo,
// which are dark enough themselves to need white. This is the same rule the
// event icon endpoint (hack_icons.go) has always used for its fallback
// avatar; both call this one function so the two never drift apart.
func hackEventInk(color string) string {
	if color == "#267b79" || color == "#4a49a8" {
		return "#fff"
	}
	return "#141413"
}
