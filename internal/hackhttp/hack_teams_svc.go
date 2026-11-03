package hackhttp

import "github.com/vsriram/simple-host/internal/app/hack/teams"

func (h *HackHandler) teamsSvc() *teams.Service { return teams.New(h.database) }
