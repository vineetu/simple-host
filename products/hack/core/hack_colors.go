package core

import (
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/model"
)

func EventColor(slug, choice string) string {
	for _, c := range events.Crayons {
		if choice == c.Name {
			return c.Color
		}
	}
	// A stable rolling index.
	index := 0
	for _, c := range slug {
		index += int(c)
	}
	return events.Crayons[index%len(events.Crayons)].Color
}

func EventAccent(ev model.Event) string { return EventColor(ev.Slug, ev.AccentColor) }

// hackEventInk is the ink colour that reads on a raw --ev background: every
// crayon is dark enough to need the kit's dark ink except teal and indigo,
// which are dark enough themselves to need white.
func EventInk(color string) string {
	if color == "#267b79" || color == "#4a49a8" {
		return "#fff"
	}
	return "#141413"
}
