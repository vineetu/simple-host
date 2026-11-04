package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/repository/hackteams"
	"github.com/vsriram/simple-host/internal/repository/sites"
)

// domainColumns is the column list scanDomainInfo reads, in order.
const domainColumns = sites.DomainColumns

// CreateUser forwards to the accounts repository.
func CreateUser(ctx context.Context, q Querier, username, apiKey string, isAdmin bool) (User, error) {
	return accounts.CreateUser(ctx, q, username, apiKey, isAdmin)
}

// HashAPIKey forwards to the accounts repository.
func HashAPIKey(key string) string {
	return accounts.HashAPIKey(key)
}

// GetUserByID forwards to the accounts repository.
func GetUserByID(ctx context.Context, q Querier, id string) (User, error) {
	return accounts.GetUserByID(ctx, q, id)
}

// GetUserByUsername forwards to the accounts repository.
func GetUserByUsername(ctx context.Context, q Querier, username string) (User, error) {
	return accounts.GetUserByUsername(ctx, q, username)
}

// ClaimHandle forwards to the accounts repository.
func ClaimHandle(ctx context.Context, q Querier, userID, handle string) (bool, error) {
	return accounts.ClaimHandle(ctx, q, userID, handle)
}

// isUniqueViolation forwards to the accounts repository.
func isUniqueViolation(err error) bool {
	return accounts.IsUniqueViolation(err)
}

// MarkEventAccount forwards to the accounts repository.
func MarkEventAccount(ctx context.Context, q Querier, userID string) error {
	return accounts.MarkEventAccount(ctx, q, userID)
}

// SetUserSuspended forwards to the accounts repository.
func SetUserSuspended(ctx context.Context, q Querier, userID, reason string) error {
	return accounts.SetUserSuspended(ctx, q, userID, reason)
}

// UserSuspended forwards to the accounts repository.
func UserSuspended(ctx context.Context, q Querier, userID string) (bool, error) {
	return accounts.UserSuspended(ctx, q, userID)
}

// TouchVisitorSession forwards to the accounts repository.
func TouchVisitorSession(ctx context.Context, q Querier, id []byte, idle time.Duration) error {
	return accounts.TouchVisitorSession(ctx, q, id, idle)
}

// EndVisitorSessions forwards to the accounts repository.
func EndVisitorSessions(ctx context.Context, q Querier, userID string) error {
	return accounts.EndVisitorSessions(ctx, q, userID)
}

// SetPlatformDomain forwards to the accounts repository.
func SetPlatformDomain(domain string) {
	accounts.SetPlatformDomain(domain)
}

// SetPlatformDomains forwards to the accounts repository.
func SetPlatformDomains(primary string, also ...string) {
	accounts.SetPlatformDomains(primary, also...)
}

// containsString forwards to the accounts repository.
func containsString(list []string, s string) bool {
	return accounts.ContainsString(list, s)
}

// currentPlatformDomain forwards to the accounts repository.
func currentPlatformDomain() string {
	return accounts.CurrentPlatformDomain()
}

// platformDomains forwards to the accounts repository.
func platformDomains() []string {
	return accounts.PlatformDomains()
}

// platformNameLabel forwards to the accounts repository.
func platformNameLabel(host string) (string, bool) {
	return accounts.PlatformNameLabel(host)
}

// hostForms forwards to the accounts repository.
func hostForms(host string) [2]string {
	return accounts.HostForms(host)
}

// lockKey forwards to the accounts repository.
func lockKey(host string) string {
	return accounts.LockKey(host)
}

// lockPlatformName forwards to the accounts repository.
func lockPlatformName(ctx context.Context, tx *sql.Tx, host string) error {
	return accounts.LockPlatformName(ctx, tx, host)
}

// handleNameTaken forwards to the accounts repository.
func handleNameTaken(ctx context.Context, tx *sql.Tx, userID, handle string) (bool, error) {
	return accounts.HandleNameTaken(ctx, tx, userID, handle)
}

// HandleAvailable forwards to the accounts repository.
func HandleAvailable(ctx context.Context, tx *sql.Tx, userID, handle string) (bool, error) {
	return accounts.HandleAvailable(ctx, tx, userID, handle)
}

// CountSitesByUser forwards to the sites repository.
func CountSitesByUser(ctx context.Context, q Querier, userID string) (int, error) {
	return sites.CountSitesByUser(ctx, q, userID)
}

// GetSiteByID forwards to the sites repository.
func GetSiteByID(ctx context.Context, q Querier, siteID string) (Site, error) {
	return sites.GetSiteByID(ctx, q, siteID)
}

// GetSiteByUser forwards to the sites repository.
func GetSiteByUser(ctx context.Context, db *sql.DB, userID, name string) (Site, error) {
	return sites.GetSiteByUser(ctx, db, userID, name)
}

// GetSiteDomainInfo forwards to the sites repository.
func GetSiteDomainInfo(ctx context.Context, database *sql.DB, siteID string) (SiteDomainInfo, bool, error) {
	info, ok, err := sites.GetSiteDomainInfo(ctx, database, siteID)
	return SiteDomainInfo(info), ok, err
}

// SetSiteSuspended forwards to the sites repository.
func SetSiteSuspended(ctx context.Context, q Querier, siteID, reason string) error {
	return sites.SetSiteSuspended(ctx, q, siteID, reason)
}

// GetActiveSiteVersion forwards to the sites repository.
func GetActiveSiteVersion(ctx context.Context, db *sql.DB, siteID string) (Version, error) {
	return sites.GetActiveSiteVersion(ctx, db, siteID)
}

// ListSitesByUser forwards to the sites repository.
func ListSitesByUser(ctx context.Context, db *sql.DB, userID string) ([]Site, error) {
	return sites.ListSitesByUser(ctx, db, userID)
}

// scanSiteRows forwards to the sites repository.
func scanSiteRows(rows *sql.Rows) ([]Site, error) {
	return sites.ScanSiteRows(rows)
}

// scanDomainInfo forwards to the sites repository.
func scanDomainInfo(row interface{ Scan(...any) error }) (SiteDomainInfo, error) {
	info, err := sites.ScanDomainInfo(row)
	return SiteDomainInfo(info), err
}

// OrphanTeamSites forwards to the hackteams repository.
func OrphanTeamSites(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, accountID string) ([]Site, error) {
	return hackteams.OrphanTeamSites(ctx, q, accountID)
}
