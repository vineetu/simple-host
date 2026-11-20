package db

import (
	"context"
	"database/sql"
	"fmt"

	operationrepo "github.com/vsriram/simple-host/host/ops"
)

// hackColumns are what EVENTS=hosted reads (hack1-events.sql, hack2-team-sites.sql). Checked only
// on such an instance (VerifyHackSchema): every other database may lack them.
var hackColumns = map[string][]string{
	"hack_account_preferences":      {"user_id", "theme", "organiser_walkthrough_done", "judge_walkthrough_done"},
	"event_organiser_invites":       {"event_id", "token_hash", "created_by", "expires_at"},
	"event_tracks":                  {"id", "event_id", "slug", "name", "challenge", "prize"},
	"event_track_panels":            {"event_id", "track_id", "judge_id"},
	"event_content":                 {"event_id", "sponsors", "faq", "schedule", "updated_at"},
	"event_announcements":           {"id", "event_id", "title", "body", "created_at", "pinned_at"},
	"event_announcement_deliveries": {"announcement_id", "user_id", "recipient", "sent_at", "attempted_at"},
	"event_entry_receipts":          {"event_id", "team_id", "user_id", "recipient", "sent_at", "attempted_at"},
	// zx-hack-entry-reminders.sql
	"event_entry_reminders": {"event_id", "team_id", "kind", "deadline", "sent_at"},
	// 0003-hack-notices-judges-messages.sql
	"event_judge_reminders": {"event_id", "user_id", "kind", "deadline", "sent_at"},
	"event_team_messages":   {"id", "event_id", "team_id", "sender_id", "sender_name", "body", "created_at", "from_team"},
	"events": {"signup_questions", "approval_required", "id", "slug", "account_id", "created_by", "title", "stage", "organiser_name", "organisation",
		"contact_email", "purpose", "expected_participants", "tagline", "website_mode", "icon_media_type", "icon_bytes", "accent_color", "about", "rules", "prizes", "overall_prizes", "coc_text",
		"time_zone", "starts_at", "ends_at", "team_size_max", "join_code", "judge_code", "submission_deadline",
		"results_visibility", "results_published_at", "closed_at", "removal_warned_at", "sites_removed_at",
		"keep_sites", "taken_down_at", "taken_down_reason", "created_at", "updated_at",
		// hack2-team-sites.sql
		"entry_required", "gallery_open", "submissions_public",
		// hack3-judging.sql
		"organiser_judges", "judge_assignment_mode", "judges_per_team", "judging_locked_at", "judging_lock_reason", "score_mode", "tie_criterion_id", "public_scores", "public_ranks",
		"team_finding", "voting_enabled", "voting_opens_at", "voting_closes_at", "voting_eligibility", "directory_listed",
		// zp-hack-applications.sql
		"applications_open",
		// zv-hack-entry-summary.sql
		"entry_preset", "entry_rules", "judge_first",
		// 0003-hack-notices-judges-messages.sql
		"reminder_offsets", "judging_ends_at",
		// 0005-hack-entry-rules-chosen.sql
		"entry_rules_chosen",
		// 0011-hack-event-theme.sql
		"theme"},
	"event_team_requests": {"id", "event_id", "team_id", "user_id", "kind", "created_by", "created_at"},
	"event_teams": {"open", "looking_for", "track_id", "id", "event_id", "slug", "name", "code", "created_by", "created_at",
		// hack2-team-sites.sql
		"deadline_override", "pinned_version", "pinned_at", "site_taken_down_at", "site_taken_down_reason",
		// zr-hack-team-kept-after-leave.sql
		"kept_after_leave",
		// zt-hack-teams-redesign.sql. open stays listed until the drop migration.
		"locked_at", "locked_by"},
	// hack2-team-sites.sql
	"event_entries": {"team_id", "event_id", "title", "tagline", "description", "video_url", "code_url",
		"screenshot", "screenshot_type", "updated_at", "updated_by",
		// zv-hack-entry-summary.sql
		"problem", "audience", "tools", "maturity", "submitted_at", "submitted_by", "submit_auto",
		"submit_settled_at", "excluded_at", "excluded_by"},
	"event_members": {"looking", "looking_intro", "event_id", "user_id", "role", "team_id", "coc_accepted_at", "joined_at", "signup_answers", "approval_status", "approval_decided_at", "approval_decided_by",
		// zp-hack-applications.sql
		"joined_via", "city", "country", "age_range", "gender",
		// zt-hack-teams-redesign.sql
		"team_admin", "team_joined_at"},
	"event_create_log": {"user_id", "created_at"},
	// hack3-judging.sql
	"rubric_criteria":   {"id", "event_id", "position", "name", "description", "weight", "max_points", "created_at", "updated_at"},
	"event_assignments": {"id", "event_id", "judge_id", "team_id", "created_at"},
	"event_conflicts":   {"event_id", "judge_id", "team_id", "declared_by", "created_at"},
	"event_scores":      {"event_id", "judge_id", "team_id", "criterion_id", "points", "comment", "updated_at"},
	"event_results":     {"event_id", "published_at", "published_by", "full_ranking", "snapshot"},
	"event_votes":       {"event_id", "voter_email", "team_id", "created_at", "updated_at"},
	// zq-hack-leave-event.sql
	"event_former_judges": {"event_id", "user_id", "role", "counted_team_ids", "left_at"},
}

// VerifyHackSchema is VerifySchema for the hosted-events tables.
func VerifyHackSchema(ctx context.Context, database *sql.DB) error {
	if err := operationrepo.VerifyColumns(ctx, database, hackColumns); err != nil {
		return err
	}
	// Team-name uniqueness lives in an index (hack1-events.sql).
	var ok bool
	if err := database.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'event_teams_name_idx')`).Scan(&ok); err != nil {
		return fmt.Errorf("inspect schema: %w", err)
	}
	if !ok {
		return fmt.Errorf("database is behind this build; missing: index event_teams_name_idx\nRun `simple-host migrate` against this database")
	}
	return nil
}
