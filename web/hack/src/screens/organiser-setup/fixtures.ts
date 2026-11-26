// The organiser's event as the setup screens' catalogue entries see it:
// Green Campus Hack (organiser, building), with every field GET E gives an
// organiser (core_views.go eventJSON + organiserJSON). Each screen's
// catalogue file overrides what its states need.
import type { Answer } from "@kit/catalogue/harness";
import { api, detail } from "@/catalogue/fixtures";
import type { EventDetail } from "@/lib/client";

export const SLUG = "green-campus";
export const E = "/v1/hack/events/" + SLUG;
export const manage = (section: string, rest = "") => `/e/${SLUG}/manage?s=${section}${rest}`;

/** orgEvent is GET E for Green Campus Hack; ev overrides fields of the event itself. */
export function orgEvent(ev: Record<string, unknown> = {}, o: Partial<EventDetail> = {}): EventDetail {
  const d = detail(SLUG, { me: { team: null } });
  return {
    ...d,
    event: {
      ...d.event,
      tagline: "Build something that makes the campus greener.",
      about: "A weekend for students to build tools that cut waste on campus.\n\nOpen to every faculty.",
      rules: "Teams of up to four. Start from scratch on the day.",
      prizes: "Mentoring from the facilities team for the winners.",
      overall_prizes: ["Rp 3.000.000", "Rp 2.000.000", ""],
      coc_text: "",
      coc_default: "Be kind. Respect everyone. No harassment of any kind.",
      starts_at: "2026-11-28T01:00:00Z",
      ends_at: "2026-11-29T10:00:00Z",
      submission_deadline: "2026-11-29T08:00:00Z",
      judging_ends_at: null,
      team_size_max: 4,
      team_finding: true,
      applications_open: true,
      url: "https://green-campus.simple-hack.app/",
      builtin_url: "https://simple-hack.app/e/green-campus",
      website_mode: "builtin",
      icon_url: "",
      accent_color: "",
      theme: "",
      icon_media_type: "",
      taken_down: false,
      results_visibility: "ranked",
      entry_required: ["title", "tagline", "problem"],
      gallery_open: true,
      submissions_public: false,
      team_sites_ready: true,
      stages_offered: ["draft", "open", "building", "closed", "judging", "results", "archived"],
      judging_locked_at: null,
      scoring_open: false,
      tracks: [
        { id: "t1", name: "Food waste", challenge: "Cut what the canteens throw away.", prize: "" },
        { id: "t2", name: "Energy", challenge: "", prize: "Solar kit" },
      ],
      ...ev,
    },
    organiser: ({
      website_site: "ev-green-campus",
      organiser_name: "Green Campus Society",
      organisation: "Universitas Hijau",
      contact_email: "hello@greencampus.example",
      purpose: "Student society hackathon",
      expected_participants: 80,
      join_code: "GRN-4821",
      join_url: "https://simple-hack.app/join/GRN-4821",
      judge_code: "JDG-1177",
      judge_url: "https://simple-hack.app/judge/JDG-1177",
      counts: { participants: 42, teams: 11, judges: 3, on_no_team: 4, emailable_participants: 38 },
    } as EventDetail["organiser"]),
    needs_you: [],
    ...o,
  };
}

/** orgApi is the catalogue's service with Green Campus as its organiser sees it; over wins. */
export const orgApi = (over: Record<string, Answer> = {}, ev: Record<string, unknown> = {}, o: Partial<EventDetail> = {}) =>
  api({ ["GET " + E]: orgEvent(ev, o), ...over });

/** Read-only variants every setup screen shows. */
export const adminView = { admin_view: true } as Partial<EventDetail>;
export const ended = { stage: "archived" };
