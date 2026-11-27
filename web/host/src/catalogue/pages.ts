// The pages the Go server writes itself (no React): every gate and status
// page on a site address, and the public text pages. The catalogue build copies
// them to /catalogue-pages/ (vite.config.ts; the gate pages come from
// `make web-pages`), and the visual checks open them as they are.
export type PageEntry = { id: string; title: string; page: string };

const gate = (id: string, title: string): PageEntry => ({ id, title, page: `/catalogue-pages/${id}.html` });

export const pageEntries: PageEntry[] = [
  gate("gate-passcode", "Gate: enter the passcode"),
  gate("gate-passcode-wrong", "Gate: wrong passcode"),
  gate("gate-private", "Gate: private site, signed out"),
  gate("gate-ask", "Gate: signed in, not on the list (Ask for access)"),
  gate("gate-asked", "Gate: after Ask for access"),
  gate("gate-offline", "Status: offline"),
  gate("gate-takendown", "Status: taken down"),
  gate("gate-notfound", "Status: no site at this address"),
  { id: "text-privacy", title: "Text page: privacy", page: "/catalogue-pages/privacy.html" },
  { id: "text-terms", title: "Text page: terms", page: "/catalogue-pages/terms.html" },
  { id: "text-support", title: "Text page: support", page: "/catalogue-pages/support.html" },
  { id: "text-features", title: "Text page: features", page: "/catalogue-pages/features.html" },
  { id: "text-docs", title: "Text page: API docs", page: "/catalogue-pages/docs.html" },
];
