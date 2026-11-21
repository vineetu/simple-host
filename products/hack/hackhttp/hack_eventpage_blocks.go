package hackhttp

import (
	"fmt"
	"strings"

	"github.com/vsriram/simple-host/products/hack/app/events"
)

// hackOrganisedBy is "Organised by <name> (<organisation>)", with either
// part alone when the other is blank, or "" when both are.
func hackOrganisedBy(name, org string) string {
	name, org = strings.TrimSpace(name), strings.TrimSpace(org)
	switch {
	case name != "" && org != "":
		return "Organised by " + name + " (" + org + ")"
	case name == "" && org == "":
		return ""
	}
	return "Organised by " + name + org
}

// hackStagePills is the short phrase in the event header's stage pill.
var hackStagePills = map[string]string{
	"draft": "Not open yet", "open": "Sign-up is open", "building": "Teams are building",
	"closed": "Entries are closed", "judging": "Judging is under way",
	"results": "Results coming soon", "archived": "This event has ended",
}

// hackStagePill is the stage's pill; published results read "Results are out".
func hackStagePill(stage string, published bool) string {
	if stage == "results" && published {
		return "Results are out"
	}
	return hackStagePills[stage]
}

// hackStageCardInput is what the stage card is decided from: the raw stage (not
// the results layout), the organiser's name to show, the deadline as already
// formatted, and whether results or voting take the spotlight.
type hackStageCardInput struct {
	Stage, AppURL, Who, ApplyURL  string
	GalleryCount                  int
	DeadlineNote, DeadlineHeading string
	Countdown, DeadlinePassed     bool
	ResultsPublished, VotingOpen  bool
}

// hackLateStageCards are the stage cards from closed onwards; each gets the
// gallery link when there are team sites to see.
var hackLateStageCards = map[string]hackStageCardView{
	"closed":   {Kicker: "Entries are closed", Heading: "Teams have handed in their work", Body: "Winners will be posted on this page."},
	"judging":  {Kicker: "Judging is under way", Heading: "The judges are scoring every entry", Body: "Winners will be posted on this page."},
	"results":  {Kicker: "Results coming soon", Heading: "Winners will be posted here", Body: "The organisers publish the results on this page."},
	"archived": {Kicker: "This event has ended", Heading: "Thanks to everyone who took part"},
}

func buildHackStageCard(in hackStageCardInput) *hackStageCardView {
	var card *hackStageCardView
	switch in.Stage {
	case "draft":
		card = &hackStageCardView{
			Kicker:    "Not open yet",
			Heading:   "The organisers are still setting this up",
			Body:      "Details may change before sign-up opens. Check back here later.",
			Secondary: &hackCTA{Label: "Find another hackathon", URL: in.AppURL + "/directory"},
		}
	case "open":
		card = hackOpenStageCard(in)
	case "building":
		card = hackBuildingStageCard(in)
	default:
		card = hackLateStageCard(in)
	}
	if card != nil && card.Primary != nil {
		card.Primary.Strong = !in.VotingOpen
	}
	return card
}

func hackSignInCTA(appURL string) *hackCTA {
	return &hackCTA{Label: "Already joined? Sign in", URL: appURL + "/events"}
}

// hackOpenStageCard asks people to apply, or to use the organisers' join
// link when applications are off, with the entry deadline as its note.
func hackOpenStageCard(in hackStageCardInput) *hackStageCardView {
	note := ""
	if in.DeadlinePassed {
		note = "Entries are closed"
	} else if in.DeadlineNote != "" {
		note = "Entries close " + in.DeadlineNote
	}
	card := &hackStageCardView{
		Kicker: "Sign-up is open", Heading: "Join with the link from the organisers",
		Body: in.Who + " shares a join link with you, for example in a WhatsApp group or by email. " +
			"Open it and sign in with your email. Some events ask a few questions first.",
		Note: note, Secondary: hackSignInCTA(in.AppURL), CountdownInNote: in.Countdown && note != "",
	}
	if in.ApplyURL != "" {
		card.Heading = "Apply to join"
		card.Body = "Sign in with your email and fill in a short form. The organisers decide who joins, and you'll see their answer on your Simple Hack Home. " +
			"Have a join link from " + in.Who + "? Open that instead."
		card.Primary = &hackCTA{Label: "Apply", URL: in.ApplyURL}
	}
	return card
}

// hackBuildingStageCard leads with the team sites when there are any, the
// deadline in its heading, and Apply as primary or extra button.
func hackBuildingStageCard(in hackStageCardInput) *hackStageCardView {
	card := &hackStageCardView{Kicker: "Teams are building", Heading: "Building is under way",
		Body: "Sign-up is still open. Join with the link from the organisers.", Secondary: hackSignInCTA(in.AppURL),
		CountdownInBody: in.Countdown}
	if in.GalleryCount > 0 {
		card.Heading = "See what teams are making"
		card.Primary = &hackCTA{Label: fmt.Sprintf("See team sites (%d)", in.GalleryCount), URL: "#sites"}
	}
	if in.DeadlinePassed {
		card.Heading = "Entries are closed"
	} else if in.DeadlineHeading != "" {
		card.Heading = "Entries close " + in.DeadlineHeading
	}
	if in.ApplyURL == "" {
		return card
	}
	card.Body = "Sign-up is still open. Apply, or join with the link from the organisers."
	apply := &hackCTA{Label: "Apply", URL: in.ApplyURL}
	if card.Primary == nil {
		card.Primary = apply
	} else {
		card.Extra = apply
	}
	return card
}

func hackLateStageCard(in hackStageCardInput) *hackStageCardView {
	base, ok := hackLateStageCards[in.Stage]
	if !ok || events.ResultsReplaceStageCard(in.Stage, in.ResultsPublished) {
		return nil
	}
	card := base
	if in.Stage == "closed" && in.DeadlineNote != "" {
		card.Body = "Entries closed " + in.DeadlineNote + ". " + card.Body
	}
	if in.GalleryCount > 0 {
		card.Primary = &hackCTA{Label: "See what teams built", URL: "#sites"}
	}
	return &card
}

// hackOrganiserWho is who the open card says shares the join link: the
// organisation, else the organiser, else "The organisers".
func hackOrganiserWho(name, organisation string) string {
	if who := strings.TrimSpace(organisation); who != "" {
		return who
	}
	if who := strings.TrimSpace(name); who != "" {
		return who
	}
	return "The organisers"
}

func buildHackPeoplesChoice(status, voteURL string) *hackPeoplesChoiceView {
	switch status {
	case "open":
		return &hackPeoplesChoiceView{
			Body: "Pick the team you like best. One vote each. You can change it until voting closes.",
			CTA:  &hackCTA{Label: "Vote for your favourite", URL: voteURL, Strong: true},
		}
	case "upcoming":
		return &hackPeoplesChoiceView{Body: "Voting opens soon."}
	case "closed":
		return &hackPeoplesChoiceView{Body: "Voting has closed.", CTA: &hackCTA{Label: "See the vote results", URL: voteURL}}
	default:
		return nil
	}
}

// buildHackResultsBlock turns the DB-facing hackResultsView into the template-ready form: avatars, the "Winner" pill and the score-method line are decided here.
func buildHackResultsBlock(res *hackResultsView) *hackResultsBlockView {
	out := &hackResultsBlockView{Heading: "Winners"}
	if res.FullRanking {
		out.Heading = "Results"
	}
	for _, row := range res.Rows {
		out.AnyRanked = out.AnyRanked || row.Rank > 0
		out.HasCards = out.HasCards || row.Card != nil
		if row.Score != "" {
			out.ShowScoreHeader = out.ShowScoreHeader || res.FullRanking
			if out.ScoreNote == "" {
				out.ScoreNote = hackScoreNote(row.ScoreMode)
			}
		}
		out.Rows = append(out.Rows, hackResultsRowViewOf(row, res.FullRanking))
	}
	return out
}

func hackScoreNote(mode string) string {
	if mode == "normalised" {
		return "Scores are adjusted so strict and generous judges count the same."
	}
	return "Scores are the judges' average."
}

func hackResultsRowViewOf(row hackResultsRow, fullRanking bool) hackResultsRowView {
	rv := hackResultsRowView{hackResultsRow: row, Score: trimScoreZeros(row.Score), Avatar: hackInitials(row.TeamName),
		ShotLetter: hackInitials(row.TeamName), WinnerPill: !fullRanking && row.Overall, TrackPill: hackTrackPill(row)}
	if row.Card == nil {
		return rv
	}
	if fullRanking && !row.Overall && !row.TrackWinner && row.Card.Title != "" {
		rv.ShotLetter = hackInitials(row.Card.Title)
	}
	rv.MaturityLabel = hackMaturityLabel(row.Card.Maturity)
	return rv
}

func hackTrackPill(row hackResultsRow) string {
	if !row.TrackWinner {
		return ""
	}
	pill := "Track winner"
	if row.TrackName != "" {
		pill = row.TrackName + " winner"
	}
	if row.TrackPrize != "" {
		pill += " · " + row.TrackPrize
	}
	return pill
}

func hackTextParas(s string) [][]string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var paras [][]string
	for _, p := range strings.Split(s, "\n\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		paras = append(paras, strings.Split(p, "\n"))
	}
	return paras
}
