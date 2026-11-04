package storage

import (
	"github.com/vsriram/simple-host/internal/model"
)

func LegacyJSONStorable(body []byte) bool { return model.LegacyJSONStorable(body) }
