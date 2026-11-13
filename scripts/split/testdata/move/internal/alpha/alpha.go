package alpha

import (
	_ "embed"
	"example.com/fixture/internal/storage"
)

//go:embed data.txt
var Data string

func Value() string { return storage.Value() }
