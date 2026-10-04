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
