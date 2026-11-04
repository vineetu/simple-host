package sites

import rules "github.com/vsriram/simple-host/internal/rules/sites"

var (
	ValidSiteName        = rules.ValidSiteName
	ValidateShape        = rules.ValidateShape
	ValidateReserved     = rules.ValidateReserved
	HandleIsLabel        = rules.HandleIsLabel
	LabelReservedForNew  = rules.LabelReservedForNew
	LabelReserved        = rules.LabelReserved
	ReservedSubdomainSet = rules.ReservedSubdomainSet
	VisitorHandle        = rules.VisitorHandle
	UUIDShape            = rules.UUIDShape
)
