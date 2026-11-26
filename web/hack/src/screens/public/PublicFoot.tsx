// The small print under the public pages (the vote page), as the old pub-foot.
export function PublicFoot({ find }: { find?: boolean }) {
  return (
    <nav aria-label="More" className="mt-12 flex flex-wrap items-center gap-x-5 border-t border-rule pt-4 text-sm text-ink-soft">
      <span>Made with Simple Hack</span>
      {[
        ["/privacy.html", "Privacy"],
        ["/terms", "Terms"],
        ["/report", "Report a page"],
        ...(find ? [["/directory", "Find a hackathon"]] : []),
      ].map(([href, label]) => (
        <a key={href} href={href} className="inline-flex min-h-11 min-w-11 items-center justify-center text-ink-soft">
          {label}
        </a>
      ))}
    </nav>
  );
}
