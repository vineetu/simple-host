package hackhttp

import "github.com/vsriram/simple-host/products/hack/app/teams"

func (h *HackHandler) teamsSvc() *teams.Service { return teams.New(h.database) }
