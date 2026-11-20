package hackhttp

import (
	"fmt"
	"strings"
)

func hackOrganisedBy(name, org string) string {
	name = strings.TrimSpace(name)
	org = strings.TrimSpace(org)
	switch {
	case name != "" && org != "":
		return "Organised by " + name + " (" + org + ")"
	case name != "":
		return "Organised by " + name
	case org != "":
		return "Organised by " + org
	default:
		return ""
	}
}

// hackStagePill is the short phrase in the event header's stage pill
// (spec sections "Event header": "[stage pill] Sign-up is open").
func hackStagePill(stage string, published bool) string {
	switch stage {
	case "draft":
		return "Not open yet"
	case "open":
		return "Sign-up is open"
	case "building":
		return "Teams are building"
	case "closed":
		return "Entries are closed"
	case "judging":
		return "Judging is under way"
	case "results":
		if published {
			return "Results are out"
		}
		return "Results coming soon"
	case "archived":
		return "This event has ended"
	default:
		return ""
	}
}

// buildHackStageCard is the one card under the header (spec
// "primary_action_by_stage" and "sections": "Stage card: ..."). Nil for
// results/archived once results are published: the Results block replaces it.
// applyURL is empty when applications are off (D4/§4: then the card is
// exactly what it would be without this feature).
func buildHackStageCard(stage, appURL, organiserName, organisation string,
	galleryCount int, deadlineNote, deadlineHeading string, hasCountdown, deadlinePassed bool, resultsPublished bool, applyURL string) *hackStageCardView {
	signIn := &hackCTA{Label: "Already joined? Sign in", URL: appURL + "/events"}
	switch stage {
	case "draft":
		return &hackStageCardView{
			Kicker:    "Not open yet",
			Heading:   "The organisers are still setting this up",
			Body:      "Details may change before sign-up opens. Check back here later.",
			Secondary: &hackCTA{Label: "Find another hackathon", URL: appURL + "/directory"},
		}
	case "open":
		who := strings.TrimSpace(organisation)
		if who == "" {
			who = strings.TrimSpace(organiserName)
		}
		if who == "" {
			who = "The organisers"
		}
		note := ""
		switch {
		case deadlinePassed:
			note = "Entries are closed"
		case deadlineNote != "":
			note = "Entries close " + deadlineNote
		}
		if applyURL != "" {
			return &hackStageCardView{
				Kicker:  "Sign-up is open",
				Heading: "Apply to join",
				Body: "Sign in with your email and fill in a short form. The organisers decide who joins, and you'll see their answer on your Simple Hack Home. " +
					"Have a join link from " + who + "? Open that instead.",
				Primary:         &hackCTA{Label: "Apply", URL: applyURL, Strong: true},
				Secondary:       signIn,
				Note:            note,
				CountdownInNote: hasCountdown && note != "",
			}
		}
		return &hackStageCardView{
			Kicker:  "Sign-up is open",
			Heading: "Join with the link from the organisers",
			Body: who + " shares a join link with you, for example in a WhatsApp group or by email. " +
				"Open it and sign in with your email. Some events ask a few questions first.",
			Note:            note,
			Secondary:       signIn,
			CountdownInNote: hasCountdown && note != "",
		}
	case "building":
		heading := "Building is under way"
		if galleryCount > 0 {
			heading = "See what teams are making"
		}
		switch {
		case deadlinePassed:
			heading = "Entries are closed"
		case deadlineHeading != "":
			heading = "Entries close " + deadlineHeading
		}
		body := "Sign-up is still open. Join with the link from the organisers."
		if applyURL != "" {
			body = "Sign-up is still open. Apply, or join with the link from the organisers."
		}
		card := &hackStageCardView{Kicker: "Teams are building", Heading: heading, Body: body, Secondary: signIn,
			CountdownInBody: hasCountdown}
		if galleryCount > 0 {
			card.Primary = &hackCTA{Label: fmt.Sprintf("See team sites (%d)", galleryCount), URL: "#sites"}
		}
		if applyURL != "" {
			apply := &hackCTA{Label: "Apply", URL: applyURL}
			if card.Primary == nil {
				apply.Strong = true
				card.Primary = apply
			} else {
				card.Extra = apply
			}
		}
		return card
	case "closed":
		body := "Winners will be posted on this page."
		if deadlineNote != "" {
			body = "Entries closed " + deadlineNote + ". " + body
		}
		card := &hackStageCardView{Kicker: "Entries are closed", Heading: "Teams have handed in their work", Body: body}
		if galleryCount > 0 {
			card.Primary = &hackCTA{Label: "See what teams built", URL: "#sites"}
		}
		return card
	case "judging":
		card := &hackStageCardView{
			Kicker: "Judging is under way", Heading: "The judges are scoring every entry",
			Body: "Winners will be posted on this page.",
		}
		if galleryCount > 0 {
			card.Primary = &hackCTA{Label: "See what teams built", URL: "#sites"}
		}
		return card
	case "results":
		if resultsPublished {
			return nil
		}
		card := &hackStageCardView{
			Kicker: "Results coming soon", Heading: "Winners will be posted here",
			Body: "The organisers publish the results on this page.",
		}
		if galleryCount > 0 {
			card.Primary = &hackCTA{Label: "See what teams built", URL: "#sites"}
		}
		return card
	case "archived":
		if resultsPublished {
			return nil
		}
		card := &hackStageCardView{Kicker: "This event has ended", Heading: "Thanks to everyone who took part"}
		if galleryCount > 0 {
			card.Primary = &hackCTA{Label: "See what teams built", URL: "#sites"}
		}
		return card
	default:
		return nil
	}
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

// buildHackResultsBlock turns the DB-facing hackResultsView into the
// template-ready form: avatars, the "Winner" pill and the one score-method
// line are all decided here (spec "Results" section, decision on score wording).
func buildHackResultsBlock(res *hackResultsView) *hackResultsBlockView {
	out := &hackResultsBlockView{Heading: "Winners"}
	if res.FullRanking {
		out.Heading = "Results"
	}
	for _, row := range res.Rows {
		rv := hackResultsRowView{hackResultsRow: row, Score: trimScoreZeros(row.Score), Avatar: hackInitials(row.TeamName), ShotLetter: hackInitials(row.TeamName)}
		if row.Rank > 0 {
			out.AnyRanked = true
		}
		if row.Score != "" {
			out.ShowScoreHeader = out.ShowScoreHeader || res.FullRanking
			if out.ScoreNote == "" {
				switch row.ScoreMode {
				case "normalised":
					out.ScoreNote = "Scores are adjusted so strict and generous judges count the same."
				default:
					out.ScoreNote = "Scores are the judges' average."
				}
			}
		}
		trackPill := ""
		if row.TrackWinner {
			trackPill = "Track winner"
			if row.TrackName != "" {
				trackPill = row.TrackName + " winner"
			}
			if row.TrackPrize != "" {
				trackPill += " · " + row.TrackPrize
			}
		}
		if !res.FullRanking {
			// Winners-only: a row can be the overall winner, a track
			// winner, or both — markTrackWinners makes the rank-1 team the
			// winner of its own track too, so the two pills are independent
			// (review 2026-10-30 item 1; Overall is set from the snapshot
			// rank, not from publicRanks, so this holds with ranks hidden).
			if row.Overall {
				rv.WinnerPill = true
			}
			if row.TrackWinner {
				rv.TrackPill = trackPill
			}
		} else {
			rv.TrackPill = trackPill
		}
		if row.Card != nil {
			if res.FullRanking && !row.Overall && !row.TrackWinner {
				name := row.Card.Title
				if name == "" {
					name = row.TeamName
				}
				rv.ShotLetter = hackInitials(name)
			}
			rv.MaturityLabel = hackMaturityLabel(row.Card.Maturity)
			out.HasCards = true
		}
		out.Rows = append(out.Rows, rv)
	}
	return out
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
