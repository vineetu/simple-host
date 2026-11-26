// The rail's icons (the old shell.js set: no two look alike, since a narrow
// rail shows only icons). Names are navigation.json's "icon" values.
const PATHS: Record<string, string> = {
  home: "M3 10.5 10 4l7 6.5V17a1 1 0 0 1-1 1h-3.5v-5h-5v5H4a1 1 0 0 1-1-1z",
  overview: "M3 3h6v7H3zM11 3h6v4h-6zM11 9h6v8h-6zM3 12h6v5H3z",
  calendar: "M4 5h12a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zm-1 4h14M7 3v4m6-4v4",
  megaphone: "M3 8v4h3l6 4V4L6 8zm12-1.5a4 4 0 0 1 0 7",
  users: "M7.5 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM2 17c0-3 2.5-5 5.5-5s5.5 2 5.5 5m1-8a2.5 2.5 0 1 0 0-5m1.5 8c2 .4 3.5 2.2 3.5 5",
  team: "M10 8a2.4 2.4 0 1 0 0-4.8A2.4 2.4 0 0 0 10 8zM4.6 10a1.9 1.9 0 1 0 0-3.8 1.9 1.9 0 0 0 0 3.8zM15.4 10a1.9 1.9 0 1 0 0-3.8 1.9 1.9 0 0 0 0 3.8zM6.2 16.5c0-2.4 1.7-4.3 3.8-4.3s3.8 1.9 3.8 4.3M1.8 15c0-1.7 1.2-2.9 2.8-2.9M18.2 15c0-1.7-1.2-2.9-2.8-2.9",
  file: "M5 2h7l4 4v11a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1zm7 0v4h4M7 10h6M7 13h6",
  globe: "M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM2 10h16M10 2c2.2 2.2 3.3 4.9 3.3 8s-1.1 5.8-3.3 8c-2.2-2.2-3.3-4.9-3.3-8S7.8 4.2 10 2z",
  trophy: "M6 3h8v4a4 4 0 0 1-8 0zm0 1H3v1.5A2.5 2.5 0 0 0 5.5 8M14 4h3v1.5A2.5 2.5 0 0 1 14.5 8M10 11v3m-3 3h6m-5-3h4",
  queue: "M4 5h12M4 10h12M4 15h7",
  star: "M10 2.8l2.2 4.6 5 .7-3.6 3.5.9 5-4.5-2.4-4.5 2.4.9-5L2.8 8.1l5-.7z",
  rubric: "M5 3h10a1 1 0 0 1 1 1v13H4V4a1 1 0 0 1 1-1zm2 4h6M7 10h6M7 13h3",
  settings: "M10 12.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5zm6.5-2.5-.1-1.2 1.6-1.3-1.6-2.8-2 .6a6 6 0 0 0-2-1.2L12 2H8l-.4 2.1a6 6 0 0 0-2 1.2l-2-.6L2 7.5l1.6 1.3a6 6 0 0 0 0 2.4L2 12.5l1.6 2.8 2-.6a6 6 0 0 0 2 1.2L8 18h4l.4-2.1a6 6 0 0 0 2-1.2l2 .6 1.6-2.8-1.6-1.3z",
  page: "M3 4h14v12H3zM3 8h14M6 6h.01",
  entries: "M4 4h5v5H4zM11 4h5v5h-5zM4 11h5v5H4zM11 11h5v5h-5z",
  chart: "M4 16V9m4 7V5m4 11v-5m4 5V7",
  vote: "M4 10l4 4 8-8",
  people: "M10 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zm-6 8c0-3.3 2.7-5.5 6-5.5s6 2.2 6 5.5",
  more: "M5 10h.01M10 10h.01M15 10h.01",
  user: "M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM10 10.2a2.6 2.6 0 1 0 0-5.2 2.6 2.6 0 0 0 0 5.2zM5.3 15.6c.9-1.9 2.6-3 4.7-3s3.8 1.1 4.7 3",
  signout: "M8 4H4v12h4M12 6l4 4-4 4M16 10H8",
  help: "M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM8 7.5a2 2 0 1 1 2.8 1.8c-.5.3-.8.7-.8 1.2V11m0 3h.01",
  events: "M3 5h14M3 10h14M3 15h14",
  chev: "M6 8l4 4 4-4",
  collapse: "M3 4h14v12H3zM8 4v12M14 8l-2 2 2 2",
  expand: "M3 4h14v12H3zM8 4v12M12 8l2 2-2 2",
};

export function Icon({ name }: { name: string }) {
  return (
    <svg viewBox="0 0 20 20" width="20" height="20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" className="flex-none">
      <path d={PATHS[name] || PATHS.file} />
    </svg>
  );
}
