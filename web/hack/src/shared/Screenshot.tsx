// A screenshot the key may read (a team's site), fetched with the key once it
// scrolls into view, as the old ui.showImage did. Nothing shows if it fails.
// It is shown as a data: URL, not a blob: one: the page's CSP allows img-src 'self' data:.
import { useEffect, useRef, useState } from "react";
import { useBytes } from "@/app/Providers";

export function Screenshot({ path, alt = "" }: { path: string; alt?: string }) {
  const bytes = useBytes();
  const box = useRef<HTMLDivElement>(null);
  const [src, setSrc] = useState("");
  const [gone, setGone] = useState(false);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    let live = true;
    const load = () =>
      bytes(path).then(
        (b) => {
          const r = new FileReader();
          r.onload = () => live && typeof r.result === "string" && setSrc(r.result);
          r.onerror = () => live && setGone(true);
          r.readAsDataURL(b);
        },
        () => live && setGone(true),
      );
    const io = typeof IntersectionObserver === "function" ? new IntersectionObserver((seen) => seen.some((s) => s.isIntersecting) && (io!.disconnect(), load())) : null;
    if (io) io.observe(el);
    else load();
    return () => {
      live = false;
      io?.disconnect();
    };
  }, [bytes, path]);
  if (gone) return null;
  return (
    <div ref={box} className="mt-3 aspect-[16/10] overflow-hidden rounded-field border border-rule bg-paper-lo">
      {src && <img src={src} alt={alt} className="block size-full object-cover object-top" />}
    </div>
  );
}
