package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/vsriram/simple-host/db/migrations"
	"github.com/vsriram/simple-host/internal/extension"
)

type productRefusal struct{ message string }

func (e *productRefusal) Error() string { return e.message }
func refuseProduct(format string, args ...any) error {
	return &productRefusal{fmt.Sprintf(format, args...)}
}
func productExitCode(err error) int {
	var refusal *productRefusal
	if errors.As(err, &refusal) {
		return 3
	}
	return 1
}
func programProduct(exts []*extension.Extension) string {
	if len(exts) == 1 && exts[0] != nil && exts[0].Name != "" {
		return exts[0].Name
	}
	return "simple-host"
}

// checkProduct runs under the migration lock, before any database write.
// explicit permits first recording on a populated database, after validation.
func checkProduct(ctx context.Context, conn *sql.Conn, exts []*extension.Extension, explicit bool) error {
	empty, recorded, err := validateProduct(ctx, conn, exts, explicit)
	if err != nil {
		return err
	}
	if !empty && !recorded && !explicit {
		return refuseProduct("this database has no product record; run `%s migrate -record-product` once", programProduct(exts))
	}
	return nil
}

// validateProduct is the read-only phase, also used before upgrade compatibility
// checks. Evidence is consulted only for explicit adoption, never for a fresh DB.
func validateProduct(ctx context.Context, conn *sql.Conn, exts []*extension.Extension, evidence bool) (empty, recorded bool, err error) {
	var users, table bool
	if err = conn.QueryRowContext(ctx, `SELECT to_regclass('public.users') IS NOT NULL, to_regclass('public.install_product') IS NOT NULL`).Scan(&users, &table); err != nil {
		return
	}
	empty = !users
	self := programProduct(exts)
	if table {
		var product string
		err = conn.QueryRowContext(ctx, `SELECT product FROM public.install_product WHERE "only"`).Scan(&product)
		if err == nil {
			recorded = true
			if product != self {
				err = refuseProduct("this install was set up as %s; this program is %s", product, self)
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		if err != nil {
			return
		}
	}
	if empty || !evidence {
		return
	}
	if self == "simple-host" {
		var hasRows bool
		hasRows, err = productEvidence(ctx, conn)
		if err == nil && hasRows {
			err = refuseProduct("this database has event rows; cannot record %s", self)
		}
	} else if exts[0].ValidateProduct != nil {
		if e := exts[0].ValidateProduct(ctx, conn); e != nil {
			err = refuseProduct("%s", e)
		}
	}
	return
}

// productEvidence is the sole platform probe of a product table. See the
// D3-S6a ruling in refactor-board/NOTES.md, 2026-11-16 07:20 UTC.
func productEvidence(ctx context.Context, conn *sql.Conn) (bool, error) {
	var exists bool
	if err := conn.QueryRowContext(ctx, `SELECT to_regclass('public.events') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		return false, err
	}
	var rows bool
	err := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM public.events)`).Scan(&rows)
	return rows, err
}

// recordProduct is the write phase; the caller holds the migration lock and
// has already passed validateProduct (and, for upgrade, the compatibility check).
func recordProduct(ctx context.Context, conn *sql.Conn, set migrations.Set, exts []*extension.Extension, logf func(string, ...any)) error {
	if _, err := set.ApplyConn(ctx, conn, logf); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO install_product (product) VALUES ($1) ON CONFLICT DO NOTHING`, programProduct(exts)); err != nil {
		return err
	}
	return checkProduct(ctx, conn, exts, false)
}
