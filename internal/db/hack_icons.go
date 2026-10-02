package db

import "context"

func SetEventIcon(ctx context.Context, q Querier, eventID, mediaType string, body []byte) error {
	_, err := q.ExecContext(ctx, `UPDATE events SET icon_media_type = $2, icon_bytes = $3, updated_at = now() WHERE id = $1`, eventID, mediaType, body)
	return err
}

func GetEventIcon(ctx context.Context, q Querier, eventID string) (string, []byte, error) {
	var mediaType string
	var body []byte
	err := q.QueryRowContext(ctx, `SELECT icon_media_type, icon_bytes FROM events WHERE id = $1`, eventID).Scan(&mediaType, &body)
	return mediaType, body, err
}
