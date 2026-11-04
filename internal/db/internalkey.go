package db

import (
	"github.com/vsriram/simple-host/internal/repository/accounts"
)

type InternalVisitorPass = accounts.InternalVisitorPass

func IssueInternalKey(userID string) (string, func(), error) {
	return accounts.IssueInternalKey(userID)
}
func IssueInternalTeamKey(userID, teamID string) (string, func(), error) {
	return accounts.IssueInternalTeamKey(userID, teamID)
}
func IssueInternalVisitorKey(pass InternalVisitorPass) (string, func(), error) {
	return accounts.IssueInternalVisitorKey(pass)
}
func LookupInternalVisitorKey(key string) (InternalVisitorPass, bool) {
	return accounts.LookupInternalVisitorKey(key)
}
func lookupInternalCred(key string) (accounts.InternalCred, bool) {
	return accounts.LookupInternalCred(key)
}

const internalKeyPrefix = "shint_"
