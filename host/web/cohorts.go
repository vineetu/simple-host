package web

import (
	operations "github.com/vsriram/simple-host/host/ops"
)

type SignupGroup = operations.SignupGroup

func CohortSources() map[string]operations.SignupGroup {
	out := make(map[string]operations.SignupGroup, len(ArrivalSources))
	for k, v := range ArrivalSources {
		out[k] = operations.SignupGroup{Key: k, Label: v.Label, Where: v.Where}
	}
	return out
}
