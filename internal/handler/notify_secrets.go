package handler

import (
	"context"
	"database/sql"
	"encoding/base64"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
)

func (h *SiteHandler) NotificationsReady() bool { return h.passcode.aead != nil }

func (p *passcodeState) sealNotification(id, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	sealed, err := p.sealWithAD("notification:"+id, plaintext)
	if err != nil {
		return "", err
	}
	return "enc1:" + base64.StdEncoding.EncodeToString(sealed), nil
}

func (p *passcodeState) openNotification(id, stored string) (string, error) {
	encoded, sealed := strings.CutPrefix(stored, "enc1:")
	if !sealed {
		return stored, nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", notifyFailure("secret_unreadable")
	}
	plain, err := p.openWithAD("notification:"+id, raw)
	if err != nil {
		return "", notifyFailure("secret_unreadable")
	}
	return plain, nil
}

// NotificationSecretsForTest is notificationSecrets, exported as a test
// hook (cleanup 3.2 slice 3): the moved hack_notifications_test.go seals a
// notification secret directly the way notify_test.go's own tests do
// in-package.
func (h *SiteHandler) NotificationSecretsForTest() db.NotificationSecrets {
	return h.notificationSecrets()
}

func (h *SiteHandler) notificationSecrets() db.NotificationSecrets {
	return db.NotificationSecrets{Seal: h.passcode.sealNotification, Open: h.passcode.openNotification}
}

// SealNotificationSecrets rewrites legacy data only; startup never migrates schema.
func (h *SiteHandler) SealNotificationSecrets(ctx context.Context, database *sql.DB) (int64, error) {
	if !h.NotificationsReady() {
		return 0, nil
	}
	return db.SealNotificationSecrets(ctx, database, h.passcode.sealNotification)
}
