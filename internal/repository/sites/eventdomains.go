package sites

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// EventDomainQuerier adds row iteration to the shared SQL interface.
type EventDomainQuerier interface {
	sqlkit.Querier
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// EventDomainRecord is a listed claim.
type EventDomainRecord struct {
	Name, Domain, IP string
	Expiry           time.Time
}

// ExpiredEventDomain is a claim snapshot used by the sweep.
type ExpiredEventDomain struct {
	Name, Domain string
	IDs          []string
	Expiry       time.Time
}

// EventNameClaimed preserves the event-domain lookup.
func EventNameClaimed(ctx context.Context, q sqlkit.Querier, name, zone string) (bool, error) {
	var value bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_domains WHERE name = $1 AND domain = $2 AND expires_at > now())`, name, zone).Scan(&value)
	return value, err
}

// EventNameLocal preserves the event-domain lookup.
func EventNameLocal(ctx context.Context, q sqlkit.Querier, name string) (bool, error) {
	var value bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM users WHERE handle = $1)
		    OR EXISTS (SELECT 1 FROM sites WHERE name = $1)`, name).Scan(&value)
	return value, err
}

// EventNameOwned preserves the event-domain lookup.
func EventNameOwned(ctx context.Context, q sqlkit.Querier, name, domain, userID string) (bool, error) {
	var value bool
	err := q.QueryRowContext(ctx, `SELECT true FROM event_domains WHERE name=$1 AND domain=$2 AND user_id=$3`, name, domain, userID).Scan(&value)
	return value, err
}

// EventCurrentIP preserves the event-domain lookup.
func EventCurrentIP(ctx context.Context, q sqlkit.Querier, name, domain, userID string) (string, error) {
	var value string
	err := q.QueryRowContext(ctx, `SELECT ip FROM event_domains WHERE name=$1 AND domain=$2 AND user_id=$3`, name, domain, userID).Scan(&value)
	return value, err
}

// EventHoldingCount preserves the event-domain lookup.
func EventHoldingCount(ctx context.Context, q sqlkit.Querier, userID, name, domain string) (int, error) {
	var value int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM event_domains WHERE user_id=$1 AND NOT (name=$2 AND domain=$3)`, userID, name, domain).Scan(&value)
	return value, err
}

// ClaimEventDomain acquires or renews a name for its owner.
func ClaimEventDomain(ctx context.Context, q sqlkit.Querier, name, domain, userID, ip, ttl string) (string, []string, bool, error) {
	var ownerID string
	var ids []string
	var wasNew bool
	err := q.QueryRowContext(ctx, `
		INSERT INTO event_domains (name, domain, user_id, ip, expires_at)
		VALUES ($1,$2,$3,$4, now() + $5::interval)
		ON CONFLICT (name, domain) DO UPDATE
		  SET ip = EXCLUDED.ip, expires_at = EXCLUDED.expires_at
		  WHERE event_domains.user_id = EXCLUDED.user_id
		RETURNING user_id, record_ids, (xmax = 0)`, name, domain, userID, ip, ttl).Scan(&ownerID, pq.Array(&ids), &wasNew)
	return ownerID, ids, wasNew, err
}

// DeleteEventDomain removes a claim after its DNS records are gone.
func DeleteEventDomain(ctx context.Context, q sqlkit.Querier, name, domain string) (sql.Result, error) {
	return q.ExecContext(ctx, `DELETE FROM event_domains WHERE name=$1 AND domain=$2`, name, domain)
}

// SetEventRecordIDs records the DNS records belonging to a claim.
func SetEventRecordIDs(ctx context.Context, q sqlkit.Querier, name, domain string, ids []string) (sql.Result, error) {
	return q.ExecContext(ctx, `UPDATE event_domains SET record_ids=$3 WHERE name=$1 AND domain=$2`, name, domain, pq.Array(ids))
}

// EventDomainOwner reads the owner and DNS records of a claim.
func EventDomainOwner(ctx context.Context, q sqlkit.Querier, name, domain string) (string, []string, error) {
	var owner string
	var ids []string
	err := q.QueryRowContext(ctx, `SELECT user_id, record_ids FROM event_domains WHERE name=$1 AND domain=$2`, name, domain).Scan(&owner, pq.Array(&ids))
	return owner, ids, err
}

// ListEventDomains lists an account's claims in creation order.
func ListEventDomains(ctx context.Context, q EventDomainQuerier, userID string) ([]EventDomainRecord, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT name, domain, ip, expires_at FROM event_domains WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventDomainRecord{}
	for rows.Next() {
		var name, domain, ip string
		var exp time.Time
		if err := rows.Scan(&name, &domain, &ip, &exp); err != nil {
			continue
		}
		out = append(out, EventDomainRecord{Name: name, Domain: domain, IP: ip, Expiry: exp})
	}
	return out, nil
}

// ExpiredEventDomains snapshots claims due for removal.
func ExpiredEventDomains(ctx context.Context, q EventDomainQuerier) ([]ExpiredEventDomain, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT name, domain, record_ids, expires_at FROM event_domains WHERE expires_at < now()`)
	if err != nil {
		return nil, err
	}
	var list []ExpiredEventDomain
	for rows.Next() {
		var e ExpiredEventDomain
		if err := rows.Scan(&e.Name, &e.Domain, pq.Array(&e.IDs), &e.Expiry); err == nil {
			list = append(list, e)
		}
	}
	rows.Close()
	return list, nil
}

// DeleteExpiredEventDomain deletes only the unchanged snapshot.
func DeleteExpiredEventDomain(ctx context.Context, q sqlkit.Querier, e ExpiredEventDomain) (sql.Result, error) {
	return q.ExecContext(ctx, `DELETE FROM event_domains
			  WHERE name=$1 AND domain=$2 AND expires_at=$3 AND record_ids=$4`, e.Name, e.Domain, e.Expiry, pq.Array(e.IDs))
}
