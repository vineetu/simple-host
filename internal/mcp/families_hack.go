package mcp

import (
	"fmt"
	"strings"
)

// This file is what is left of the Simple Hack composition until D3-S1c moves
// the screens-in-chat. The product supplies its four base families
// (products/hack/mcp); the app-only family and the screens still live in this
// package and are added to a product's contribution here.

// NewServer builds the tool server. A nil or zero contribution builds only the
// platform's tools.
func NewServer(cfg Config) *Server {
	cfg.Contribution = withScreens(cfg.Contribution)
	return newServer(cfg)
}

// Inventory returns the composed documentation inventory with Events enabled.
func Inventory(product Contribution) []InventoryTool {
	limits := DefaultLimits()
	return inventoryTools(Config{Limits: &limits, Events: inventoryEvents{}, Contribution: withScreens(&product)})
}

// withScreens appends the app-only family, built from the contributed base
// tools, after the product's own families, and supplies the screens. Only a
// contribution with the Hack events family gets them: the app-only tools are
// twins of that product's base tools. Any other is returned as it is.
func withScreens(product *Contribution) *Contribution {
	if product == nil {
		return product
	}
	hack := false
	var base []Tool
	for _, family := range product.Families {
		hack = hack || family.Family == famHackEvents
		base = append(base, family.Tools...)
	}
	if !hack {
		return product
	}
	out := *product
	out.Families = append(append([]familyTools(nil), product.Families...), familyTools{Family: famHackAppOnly, Tools: hackAppFamilyTools(base)})
	if out.Apps == nil {
		out.Apps = hackApps
	}
	return &out
}

func hackApps(ctx AppContext) AppContribution {
	// The screens retain their existing methods until D3-S1c. This adapter
	// owns only immutable context; it never receives the serving Server.
	screens := &Server{cfg: Config{APIHost: ctx.APIHost}}
	if ctx.Fixed {
		screens.fixed = &inventory{}
	}
	return AppContribution{
		Screens: screens.hackEnabledScreens,
		Resources: func(c Caller, allowed bool) []map[string]any {
			out := []map[string]any{}
			for _, resource := range screens.hackAppResourcesWithMeta(c, allowed, ctx.Meta) {
				out = append(out, resource.(map[string]any))
			}
			return out
		},
		Gate: screens.hackAppsGate,
		Read: func(req Request, c Caller, allowed bool) (Response, bool) {
			return screens.readHackAppWithMeta(req, c, allowed, ctx.Meta), true
		},
	}
}

// The three helpers below are the screens' copies of the product's own
// (products/hack/mcp); they leave with the screens in D3-S1c.

func eventSlug(args map[string]any) (string, error) {
	slug, err := stringArg(args, "slug")
	if err != nil {
		return "", err
	}
	return strings.ToLower(slug), nil
}

func hackRestError(tool string, u upstreamResult) error {
	msg, code := upstreamMessage(u)
	switch code {
	case "invalid_name":
		return fmt.Errorf("%s failed (HTTP %d): %s [%s]\nEvent names are 3 to 39 lowercase letters, numbers or hyphens, and cannot start or end with a hyphen. Correct the name and call again.", tool, u.status, msg, code)
	case "name_reserved":
		return fmt.Errorf("%s failed (HTTP %d): %s [%s]\nThat event name is reserved. Ask the person for a different name.", tool, u.status, msg, code)
	default:
		return restError(tool, u)
	}
}

func hackAbilitiesSchema() map[string]any {
	return outObject(map[string]any{"manage": outBool("May manage the event."), "judge": outBool("May enter judging."), "take_part": outBool("May enter the participant area.")}, "manage", "judge", "take_part")
}
