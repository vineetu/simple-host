package model

import (
	"database/sql"
	"errors"
	"time"
)

type AddressFamily struct {
	ID              string
	UserID          string
	Suffix          string
	SitePrefix      string
	Rank            int
	Canonical       bool
	Token           string
	Status          string // pending | active | failing
	LastError       string
	BoundAt         time.Time
	VerifiedAt      sql.NullTime
	CheckedAt       sql.NullTime
	FailingSince    sql.NullTime
	LapseNotifiedAt sql.NullTime
	ReleaseAt       sql.NullTime
	CertMode        string // wildcard | per_host
	CertName        string
	ProofExempt     bool
	CreatedAt       time.Time
	// Handle and Email are the owner's (filled by every read here).
	Handle string
	Email  string
}
type FamilyBind struct {
	UserID      string
	Suffix      string
	SitePrefix  string
	Rank        int
	Canonical   bool
	CertMode    string
	CertName    string
	ProofExempt bool
	// Max is the account's cap (ADDRESS_FAMILIES_PER_ACCOUNT); 0 = none allowed.
	Max int
}
type FamilyPatch struct {
	SitePrefix *string
	Rank       *int
	Canonical  *bool
}
type FamilyCheck struct {
	FailingSince sql.NullTime
	Notified     bool
	ReleaseAt    sql.NullTime
}

func (f AddressFamily) Verified() bool { return f.VerifiedAt.Valid }

var (
	// ErrFamilyTaken: another account holds the name (a verified family
	// that overlaps it, or a custom domain under it).
	ErrFamilyTaken = errors.New("that name is connected to another account")
	// ErrFamilyExists: this account already has this family.
	ErrFamilyExists = errors.New("this family is already connected")
	// ErrFamilyCap: the account has as many families as it may.
	ErrFamilyCap = errors.New("too many address families")
)
