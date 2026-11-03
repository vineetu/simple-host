package handler

import "github.com/vsriram/simple-host/internal/rules/sites"

// Transitional (cleanup 3.2): callers move to rules/sites in 4.x.
//
// This file re-exposes internal/rules/sites's pure name rules under the
// names internal/handler used before the move, so existing call sites
// (including internal/handler/hack*.go, owned by a parallel worker in this
// step) keep compiling unchanged.
var (
	validSiteName        = sites.ValidSiteName
	validateSiteShape    = sites.ValidateShape
	validateSiteReserved = sites.ValidateReserved
	handleIsLabel        = sites.HandleIsLabel
	labelReservedForNew  = sites.LabelReservedForNew
	labelReserved        = sites.LabelReserved
	reservedSubdomainSet = sites.ReservedSubdomainSet
	visitorHandleRe      = sites.VisitorHandle
	uuidShape            = sites.UUIDShape
)
