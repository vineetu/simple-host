package db

import (
	"github.com/vsriram/simple-host/internal/repository/hackevents"
)

var (
	// GetEventBySlug is used by internal/handler.
	GetEventBySlug = hackevents.GetEventBySlug
	// GetEventByID is used by internal/handler.
	GetEventByID = hackevents.GetEventByID
	// GetEventByAccount is used by internal/handler.
	GetEventByAccount = hackevents.GetEventByAccount
	// TakeDownEvent is used by internal/handler.
	TakeDownEvent = hackevents.TakeDownEvent
	// GetEventMember is used by internal/handler.
	GetEventMember = hackevents.GetEventMember
)

// SetEventIcon is used by internal/handler.
var SetEventIcon = hackevents.SetEventIcon

// GetEventIcon is used by internal/handler.
var GetEventIcon = hackevents.GetEventIcon

// EventAccountFlag is used by internal/handler.
var EventAccountFlag = hackevents.EventAccountFlag

// HackNotificationAllowed is used by internal/handler.
var HackNotificationAllowed = hackevents.HackNotificationAllowed

// HackNotificationContextExists is used by internal/handler.
var HackNotificationContextExists = hackevents.HackNotificationContextExists
