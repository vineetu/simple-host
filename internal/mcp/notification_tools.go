package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

func notificationSchema() map[string]any {
	props := map[string]any{
		"id":               outString("The subscription ID. Use it with remove_notification."),
		"site":             outString(outSiteName),
		"url":              outString("The webhook scheme and host, with /... when a path is present."),
		"events":           outArray("The event types this subscription receives.", outString("An event name.")),
		"storage":          outString("The storage resource, or an empty string for all resources."),
		"label":            outString("The owner's label for the subscription."),
		"source":           outEnum("Who created the subscription.", "mcp", "owner"),
		"app_name":         outString("The connected app's name, when created through OAuth."),
		"state":            outEnum("The subscription's current state.", "active", "paused", "suspended", "pending", "expired"),
		"created_at":       outString("When the subscription was created (RFC 3339)."),
		"last_result":      outString("The latest delivery result category."),
		"auth_header_name": outString("The optional owner header's name. Its value is never returned."),
	}
	for _, key := range []string{"expires_at", "last_attempt_at", "last_success_at", "failing_since"} {
		props[key] = map[string]any{"type": []string{"string", "null"}, "description": "An RFC 3339 timestamp, or null when unset."}
	}
	return outObject(props, "id", "site", "url", "events", "storage", "label", "source", "app_name", "state", "created_at")
}

func notificationLimitsSchema() map[string]any {
	return outObject(map[string]any{
		"per_site":     outInteger("Maximum subscriptions per site."),
		"per_minute":   outInteger("Maximum events per site per minute."),
		"per_day":      outInteger("Maximum events per site per day."),
		"history_days": outInteger("Days of delivery history kept."),
	}, "per_site", "per_minute", "per_day", "history_days")
}

func notificationTools() []Tool {
	return []Tool{
		{
			Name: "list_notifications", Title: "List notifications",
			Description: "List webhook subscriptions and their delivery status for one site, or every site on the account. Includes subscriptions created by connected apps.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}),
			OutputSchema: outObject(map[string]any{
				"notifications": outArray("The account's webhook subscriptions.", notificationSchema()),
				"limits":        notificationLimitsSchema(),
			}, "notifications", "limits"),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				path := "/v1/notifications"
				if _, present := args["site"]; present {
					site, err := siteArg(args)
					if err != nil {
						return output{}, err
					}
					path = "/v1/sites/" + url.PathEscape(site) + "/notifications"
				}
				res := c.do(http.MethodGet, path, nil, nil)
				if !res.ok() {
					return output{}, restError("list_notifications", res)
				}
				var body struct {
					Notifications []map[string]any `json:"notifications"`
					Limits        map[string]any   `json:"limits"`
				}
				if json.Unmarshal(res.body, &body) != nil {
					return output{}, errors.New("list_notifications: server returned invalid JSON")
				}
				notifications := make([]any, 0, len(body.Notifications))
				for _, row := range body.Notifications {
					// Only public subscription fields belong in a tool result.
					item := map[string]any{}
					for _, key := range []string{"id", "site", "url", "events", "storage", "label", "source", "app_name", "state", "created_at", "expires_at", "last_attempt_at", "last_success_at", "last_result", "failing_since", "auth_header_name"} {
						if value, present := row[key]; present {
							item[key] = value
						}
					}
					item["url"] = ""
					if raw, ok := row["url"].(string); ok {
						if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
							item["url"] = u.Scheme + "://" + u.Host
							if u.Path != "" {
								item["url"] = item["url"].(string) + "/..."
							}
						}
					}
					notifications = append(notifications, item)
				}
				limits := map[string]any{}
				for _, key := range []string{"per_site", "per_minute", "per_day", "history_days"} {
					limits[key] = body.Limits[key]
				}
				return output{Text: fmt.Sprintf("%d notification subscriptions.", len(notifications)),
					Structured: map[string]any{"notifications": notifications, "limits": limits}}, nil
			},
		},
		{
			Name: "remove_notification", Title: "Remove notification",
			Description:  "Remove a webhook subscription from a site and stop its deliveries. Use the ID from list_notifications.",
			InputSchema:  object(map[string]any{"site": str(siteDesc), "id": str("The subscription ID from list_notifications.")}, "site", "id"),
			OutputSchema: outObject(map[string]any{"deleted": outBool("Whether the subscription was removed.")}, "deleted"),
			Annotations:  writes(true, true, false),
			run: func(c *call, args map[string]any) (output, error) {
				site, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				id, err := stringArg(args, "id")
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodDelete, "/v1/sites/"+url.PathEscape(site)+"/notifications/"+url.PathEscape(id), nil, nil)
				if !res.ok() {
					return output{}, restError("remove_notification", res)
				}
				return output{Text: "Notification removed.", Structured: map[string]any{"deleted": true}}, nil
			},
		},
	}
}
