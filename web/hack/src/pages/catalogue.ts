// The pages the Go server writes itself (no React): the public event page in
// every stage and every Look, the marketing pages and the status pages. `make
// hack-web-pages` renders them (products/hack/catalogue_pages_test.go and
// products/hack/public/catalogue_pages_test.go); the catalogue build puts them
// under /catalogue-pages/ (vite.config.ts), and the visual checks open them as
// they are, at the catalogue's clock.
export type PageEntry = {
  id: string;
  title: string;
  page: string;
  /** Console errors the page makes on purpose (a lookup that answers 404 on an install without it). */
  allow?: RegExp;
};

const page = (id: string, title: string, extra: Partial<PageEntry> = {}): PageEntry => ({ id, title, page: `/catalogue-pages/${id}.html`, ...extra });

const looks = ["classic", "poster", "confetti", "boardroom", "garden", "terminal"];
const lookName = (l: string) => l[0].toUpperCase() + l.slice(1);

export const pageEntries: PageEntry[] = [
  ...looks.flatMap((l) => [
    page(`event-${l}-open`, `Event page, ${lookName(l)} Look: sign-up open`),
    page(`event-${l}-results`, `Event page, ${lookName(l)} Look: results`),
  ]),
  page("event-classic-draft", "Event page: draft (organisers' preview)"),
  page("event-classic-building", "Event page: building, with the countdown"),
  page("event-classic-judging", "Event page: judging, with submissions"),
  page("event-classic-winners", "Event page: winners only"),
  page("event-classic-bare", "Event page: nothing filled in yet"),
  page("event-takendown", "Event page: taken down"),
  page("event-notfound", "Event page: no such event"),
  page("home", "Home"),
  page("hackathons", "Hackathons"),
  page("get-started", "Get started"),
  page("companies", "For companies"),
  page("watch", "Watch how it works", { allow: /Failed to load resource: the server responded with a status of 404/ }),
  page("features", "Features"),
  page("setup", "Self-host setup"),
  page("notfound", "Not found (404)"),
  page("status-takendown", "Status: taken down"),
  page("status-offline", "Status: offline"),
  page("status-message", "Status: one message (a used link)"),
  page("status-error", "Status: service error"),
];
