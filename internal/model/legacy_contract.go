package model

import (
	"errors"
)

const (
	KindContent    = "content"
	KindEntries    = "entries"
	KindPersonal   = "mine"
	KindBoard      = "board"
	NotifyOff      = "off"
	NotifyEach     = "each"
	NotifyDaily    = "daily"
	SaversAnyone   = "anyone"
	SaversListed   = "listed"
	HistoryState   = "state"
	HistoryList    = "list"
	HistoryStorage = "storage"
	OpReplace      = "replace"
	OpChange       = "change"
	OpEdit         = "edit"
	OpDelete       = "delete"
	OpClear        = "clear"
	OpUndelete     = "undelete"
	OpRestore      = "restore"
	OperatorLabel  = "Simple Host (operator)"
)

var ErrNameHasRows = errors.New("the name holds items")
var ErrSeveralItems = errors.New("the name holds several items")

type PrivateItemsError struct {
	Live, Deleted int64
	Content       bool
}

func (e *PrivateItemsError) Error() string { return "the private name holds items" }

var ErrKindChanged = errors.New("the name's kind changed")

var ErrLegacyEntryTooLarge = errors.New("too large")
var ErrLegacyEntryNotObject = errors.New("not an object")
