// People: the organiser's applications and everyone in the event
// (GET E/applications, GET E/people), as the old organiser-people.js reads them.

export type Application = {
  user_id: string;
  display_name: string;
  email?: string;
  status: "pending" | "approved" | "rejected" | string;
  joined_via?: "link" | "applied" | string;
  joined_at?: string;
  city?: string;
  country?: string;
  age_range?: string;
  gender?: string;
  answers?: { prompt: string; answer?: string }[];
};

export type Person = {
  user_id: string;
  display_name?: string;
  role: "organiser" | "participant" | "judge";
  primary_organiser?: boolean;
  approval_status?: string;
  team?: { slug?: string; name?: string } | null;
  joined_at?: string;
};

export const AGE_RANGE_LABELS: Record<string, string> = { under_18: "Under 18", "18_24": "18-24", "25_34": "25-34", "35_plus": "35+" };
export const GENDER_LABELS: Record<string, string> = {
  woman: "Woman or girl",
  man: "Man or boy",
  non_binary: "Non-binary",
  another: "Another gender",
  prefer_not_to_say: "Prefer not to say",
};

/** countryName is an ISO code as a name in the browser's language, or the bare code. */
export function countryName(code?: string): string {
  if (!code) return "";
  for (const langs of [[navigator.language], ["en"]])
    try {
      return new Intl.DisplayNames(langs, { type: "region" }).of(code) || code;
    } catch {
      /* try the next */
    }
  return code;
}

export function personCategory(p: Person): string {
  if (p.role === "organiser") return p.primary_organiser ? "Organiser" : "Co-organiser";
  if (p.role === "judge") return "Judge";
  return "Participant";
}

export const ROLE_FILTERS = ["Everyone", "Organisers", "Participants", "Judges", "Co-organisers"] as const;
export type RoleFilter = (typeof ROLE_FILTERS)[number];

export function matchesRole(p: Person, f: RoleFilter): boolean {
  switch (f) {
    case "Everyone":
      return true;
    case "Organisers":
      return p.role === "organiser";
    case "Participants":
      return p.role === "participant";
    case "Judges":
      return p.role === "judge";
    case "Co-organisers":
      return p.role === "organiser" && !p.primary_organiser;
  }
}

export type AppFilters = { from: "all" | "applied" | "link"; age: string; country: string; sort: "oldest" | "newest" };
export const NO_FILTERS: AppFilters = { from: "all", age: "", country: "", sort: "oldest" };

export const filtersActive = (f: AppFilters) => f.from !== "all" || f.age !== "" || f.country !== "" || f.sort !== "oldest";

/** visibleApplications is the waiting list through the filters (the API gives them oldest first; newest is that reversed). */
export function visibleApplications(pending: Application[], f: AppFilters): Application[] {
  let out = pending.slice();
  if (f.from === "applied") out = out.filter((a) => a.joined_via !== "link");
  else if (f.from === "link") out = out.filter((a) => a.joined_via === "link");
  if (f.age) out = out.filter((a) => a.age_range === f.age);
  if (f.country) out = out.filter((a) => a.country === f.country);
  if (f.sort === "newest") out.reverse();
  return out;
}

/** personLine is the line under a person's name: what they are, their team, and when they joined (or applied, if not let in). */
export function personBits(p: Person, when: string): string {
  const bits = [personCategory(p)];
  const notApproved = p.approval_status === "pending" || p.approval_status === "rejected";
  if (p.approval_status === "rejected") bits.push("Not approved");
  if (p.team && (p.team.name || p.team.slug)) bits.push((p.team.name || p.team.slug)!);
  if (when) bits.push((notApproved ? "Applied " : "Joined ") + when);
  return bits.join(" · ");
}

export function removeBody(name: string, role: string): string {
  return role === "judge"
    ? name + " is removed from the event. They can rejoin with the current join link unless you regenerate it."
    : name +
        " and their team membership are removed. They can rejoin with the current join link unless you regenerate it, or apply again while applications are on — you’d decide on that application.";
}

export const rejectBody = (name: string) =>
  `${name} can't join a team or take part in this event, and you can't change this decision later. They can still leave and apply again while your event takes applications, and you'd decide again.`;
