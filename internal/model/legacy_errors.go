package model

import (
	"errors"
)

var ErrSiteFull = errors.New("site saved data is full")
var ErrOnePerPerson = errors.New("one entry per person")
var ErrNameFull = errors.New("this list is full")
var ErrPeopleFull = errors.New("this Personal name has as many people as it may")
var ErrVersionConflict = errors.New("the item changed since that version")
var ErrNoEarlierValue = errors.New("this change has no earlier value to put back")
var ErrOneDocument = errors.New("page info holds one document")

// ErrStateVersionConflict means a conditional legacy document write lost its expected version.
var ErrStateVersionConflict = errors.New("state version conflict")
