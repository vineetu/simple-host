// Package recipes holds the page recipes: for each common job, the data
// setup, a working page and who can see what. Every AI app gets the same
// text, from GET /v1/recipes/{topic}.
package recipes

import (
	"embed"
	"net/http"
	"slices"
	"strings"

	"simplehost/app"
	"simplehost/core/web"
)

//go:embed text/*.md
var files embed.FS

// Topics are the recipes, one per way data is shared.
var Topics = []string{"inbox", "public", "personal", "comments", "portal", "gallery"}

// Text is a recipe with the site's name filled in.
func Text(topic, site string) (string, error) {
	if !slices.Contains(Topics, topic) {
		return "", web.Refuse(http.StatusNotFound, "no_recipe", "Ask for one of: "+strings.Join(Topics, ", ")+".")
	}
	b, err := files.ReadFile("text/" + topic + ".md")
	if site == "" {
		site = "my-site"
	}
	return strings.ReplaceAll(string(b), "{site}", site), err
}

// Mount adds the recipe route on the platform.
func Mount(a *app.App) {
	a.Routes.Platform.Handle("GET /v1/recipes/{topic}", web.Func(func(w http.ResponseWriter, r *http.Request) error {
		text, err := Text(r.PathValue("topic"), r.URL.Query().Get("site"))
		if err != nil {
			return err
		}
		web.JSON(w, http.StatusOK, map[string]string{"topic": r.PathValue("topic"), "text": text})
		return nil
	}))
}
