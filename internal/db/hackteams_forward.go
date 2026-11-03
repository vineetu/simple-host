package db

import (
	"github.com/vsriram/simple-host/internal/repository/hackteams"
)

var (
	// ListEventTeams is used by internal/handler.
	ListEventTeams = hackteams.ListEventTeams
	// GetEventTeamBySlug is used by internal/handler.
	GetEventTeamBySlug = hackteams.GetEventTeamBySlug
)

// ResolveTeamIdentity is used by internal/handler.
var ResolveTeamIdentity = hackteams.ResolveTeamIdentity

// TeamWriteStateFor is used by internal/auth, internal/handler.
var TeamWriteStateFor = hackteams.TeamWriteStateFor

// PinDueTeams is used by internal/handler.
var PinDueTeams = hackteams.PinDueTeams

// PinnedVersionOfSite is used by internal/handler.
var PinnedVersionOfSite = hackteams.PinnedVersionOfSite

// DeleteEmptyOpenTeams is used by internal/handler.
var DeleteEmptyOpenTeams = hackteams.DeleteEmptyOpenTeams

// HackSiteOwnerOnSite is used by internal/handler.
var HackSiteOwnerOnSite = hackteams.HackSiteOwnerOnSite
