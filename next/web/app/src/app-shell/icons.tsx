// The few line icons the app shell uses (24 px grid, currentColor).
const base = { viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.8, strokeLinecap: "round", strokeLinejoin: "round", "aria-hidden": true } as const;

export const SitesIcon = () => (
  <svg {...base} className="size-6">
    <rect x="3" y="4" width="18" height="16" rx="2.5" />
    <path d="M3 9h18M7 6.5h.01M10 6.5h.01" />
  </svg>
);
export const AdminIcon = () => (
  <svg {...base} className="size-6">
    <path d="M12 3l7.5 3v5.5c0 4.5-3.2 8-7.5 9.5-4.3-1.5-7.5-5-7.5-9.5V6z" />
    <path d="M9 12l2 2 4-4" />
  </svg>
);
export const DocsIcon = () => (
  <svg {...base} className="size-6">
    <path d="M5 4h9l5 5v11H5z" />
    <path d="M14 4v5h5M8 13h8M8 16.5h6" />
  </svg>
);
export const SignOutIcon = () => (
  <svg {...base} className="size-6">
    <path d="M14 4h4.5A1.5 1.5 0 0 1 20 5.5v13a1.5 1.5 0 0 1-1.5 1.5H14" />
    <path d="M10 8l-4 4 4 4M6 12h10" />
  </svg>
);
