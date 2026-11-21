package servertest

import (
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
)

func platformTestEdition() *edition.Edition { return edition.Default(config.DefaultLimits()) }
