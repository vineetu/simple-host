package storage

import (
	"github.com/vsriram/simple-host/host/model"
)

func LegacyJSONStorable(body []byte) bool { return model.LegacyJSONStorable(body) }
