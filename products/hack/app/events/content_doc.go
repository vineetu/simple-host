package events

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/sqlkit"
	model "github.com/vsriram/simple-host/products/hack/model"
)

type Sponsor struct {
	Name     string `json:"name"`
	Tier     string `json:"tier"`
	LogoData string `json:"logo_data"`
	URL      string `json:"url"`
}

type FAQ struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type ScheduleItem struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	StartAt     string `json:"start_at"`
	EndAt       string `json:"end_at"`
	// Prompt is the forward move this row lines up with (ScheduleMoves), or empty.
	Prompt string `json:"prompt,omitempty"`
	// Fixed is never stored.
	Fixed bool `json:"fixed,omitempty"`
}

type Content struct {
	Sponsors []Sponsor      `json:"sponsors"`
	FAQ      []FAQ          `json:"faq"`
	Schedule []ScheduleItem `json:"schedule"`
}

// ScheduleMoves are the forward moves a schedule row may prompt, with the button label each one shows.
var ScheduleMoves = map[string]string{
	"open":            "Open sign-up",
	"building":        "Start building",
	"closed":          "Close entries",
	"judging":         "Start judging",
	"results":         "Finish judging",
	"publish_results": "Publish results",
}

// EventContent reads the event's content document; missing lists are empty.
func (s *Service) EventContent(ctx context.Context, eventID string) (Content, error) {
	return ReadEventContent(ctx, s.db, eventID)
}

func ReadEventContent(ctx context.Context, q sqlkit.Querier, eventID string) (Content, error) {
	raw, err := ReadContent(ctx, q, eventID)
	c := Content{Sponsors: []Sponsor{}, FAQ: []FAQ{}, Schedule: []ScheduleItem{}}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(raw.Sponsors, &c.Sponsors); err != nil {
		return c, err
	}
	if err = json.Unmarshal(raw.FAQ, &c.FAQ); err != nil {
		return c, err
	}
	err = json.Unmarshal(raw.Schedule, &c.Schedule)
	return c, err
}

func (s *Service) SaveContent(ctx context.Context, ev model.Event, c Content) (Content, error) {
	if ev.Stage == "archived" {
		return c, &Error{Code: "event_ended", Message: "this event has ended"}
	}
	c, err := cleanContent(c, ev.TimeZone)
	if err != nil {
		return c, err
	}
	sponsors, _ := json.Marshal(c.Sponsors)
	faq, _ := json.Marshal(c.FAQ)
	schedule, _ := json.Marshal(c.Schedule)
	return c, s.PutContent(ctx, ev.ID, model.EventContent{Sponsors: sponsors, FAQ: faq, Schedule: schedule})
}

// cleanContent checks every item and normalises the schedule's times.
func cleanContent(c Content, zone string) (Content, error) {
	editable := []ScheduleItem{}
	for _, s := range c.Schedule {
		if !s.Fixed {
			editable = append(editable, s)
		}
	}
	c.Schedule = editable
	if len(c.Sponsors) > 20 || len(c.FAQ) > 30 || len(c.Schedule) > 100 {
		return c, &Error{Code: "invalid_content", Message: "too many items"}
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	for i := range c.Sponsors {
		if err := c.Sponsors[i].clean(); err != nil {
			return c, err
		}
	}
	for i := range c.FAQ {
		if err := c.FAQ[i].clean(); err != nil {
			return c, err
		}
	}
	for i := range c.Schedule {
		if err := c.Schedule[i].clean(loc); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (s *Sponsor) clean() (err error) {
	if s.Name, err = lineValue(s.Name, "sponsor name", 1, 100); err != nil {
		return err
	}
	if s.Tier, err = lineValue(s.Tier, "sponsor tier", 0, 60); err != nil {
		return err
	}
	if s.URL != "" && !httpsURL(s.URL) {
		return &Error{Code: "invalid_sponsor_url", Message: "use an HTTPS sponsor website"}
	}
	if !LogoValid(s.LogoData) {
		return &Error{Code: "invalid_logo", Message: "use a PNG, JPEG or WebP image under 128 KB"}
	}
	return nil
}

func (f *FAQ) clean() (err error) {
	if f.Question, err = lineValue(f.Question, "question", 1, 200); err != nil {
		return err
	}
	f.Answer, err = textValue(f.Answer, "answer", 1, 2000)
	return err
}

func (s *ScheduleItem) clean(loc *time.Location) (err error) {
	if s.Title, err = lineValue(s.Title, "schedule title", 1, 120); err != nil {
		return err
	}
	if s.Description, err = textValue(s.Description, "schedule description", 0, 500); err != nil {
		return err
	}
	start, err := ParseEventTime(s.StartAt, loc, "start_at")
	if err != nil {
		return err
	}
	end, err := ParseEventTime(s.EndAt, loc, "end_at")
	if err != nil {
		return err
	}
	if !end.Time.After(start.Time) {
		return &Error{Code: "invalid_schedule", Message: "each schedule item must end after it starts"}
	}
	if _, known := ScheduleMoves[s.Prompt]; s.Prompt != "" && !known {
		return &Error{Code: "invalid_schedule_prompt", Message: "prompt must be empty or one of open, building, closed, judging, results, publish_results"}
	}
	s.StartAt, s.EndAt = start.Time.UTC().Format(time.RFC3339), end.Time.UTC().Format(time.RFC3339)
	return nil
}

// httpsURL is an https URL with a host and no credentials, at most 500 bytes.
func httpsURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && len(s) <= 500
}

// LogoValid accepts "" or a base64 PNG, JPEG or WebP data URL of at most 128 KB whose bytes match its declared type.
func LogoValid(s string) bool {
	if s == "" {
		return true
	}
	const prefix = "data:image/"
	if !strings.HasPrefix(s, prefix) || len(s) > 180000 {
		return false
	}
	i := strings.Index(s, ";base64,")
	if i < 0 {
		return false
	}
	kind := s[len(prefix):i]
	if kind != "png" && kind != "jpeg" && kind != "webp" {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(s[i+8:])
	return err == nil && len(b) > 0 && len(b) <= 128<<10 && http.DetectContentType(b) == "image/"+kind
}
