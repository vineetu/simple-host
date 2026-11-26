// The upload link's page (route /upload/<token>): what it talks to. A link is
// checked with GET /v1/uploads/<token> ({kind: "files", namespace} or
// {kind: "archive"}) and used once with PUT /v1/uploads/<token>: every file in
// one multipart request, or the site as one archive. No sign-in: the link is
// the right. Photos are made smaller on this device first by the shared
// photo-process.js (the same code as the in-chat photo picker), which also
// drops camera and location details.
import { createContext, useContext } from "react";
import { ApiError } from "@/lib/api";

export type Link = { kind: "files" | "archive"; namespace?: string };
export type FilesSaved = { namespace: string; files: string[] };
export type Published = { version?: number; url?: string };
export type Shrunk = { blob: Blob; ext: string };

/** The most files sent at once, as the old page allowed. */
export const MAX_FILES = 30;
/** What photo-process.js aims each photo at (bytes, and the long side in pixels). */
export const PHOTO_BYTES = 1_000_000;
export const PHOTO_SIDE = 2048;

export type UploadDeps = {
  check: (token: string) => Promise<Link>;
  /** send is the one PUT; progress gets the bytes sent so far and the total. */
  send: <T>(token: string, body: Blob | FormData, type: string | null, progress: (sent: number, total: number) => void) => Promise<T>;
  shrink: (file: File) => Promise<Shrunk>;
  slug: (name: string) => string;
  /** The service's limits: one site's file stores, one site's published files. */
  filesLimit: number;
  siteLimit: number;
};

const sentence = (status: number, b: { code?: string; fix?: string }, otherwise: string) =>
  new ApiError(status, b.code || "error", b.fix || otherwise);

/** realDeps talks to the service and loads photo-process.js the first time a photo needs it. */
export const realDeps: UploadDeps = {
  async check(token) {
    let res: Response;
    try {
      res = await fetch("/v1/uploads/" + encodeURIComponent(token), { credentials: "omit", cache: "no-store" });
    } catch {
      throw new ApiError(0, "offline", "Cannot reach Simple Host. Check your connection and try again.");
    }
    const b = await res.json().catch(() => ({}));
    if (!res.ok) throw sentence(res.status, b, "This upload link does not work. Ask your chat for a new one.");
    return b as Link;
  },
  send(token, body, type, progress) {
    return new Promise((resolve, reject) => {
      const x = new XMLHttpRequest();
      x.open("PUT", "/v1/uploads/" + encodeURIComponent(token));
      if (type) x.setRequestHeader("Content-Type", type);
      x.upload.onprogress = (e) => e.lengthComputable && progress(e.loaded, e.total);
      x.onerror = () => reject(new ApiError(0, "offline", "The upload stopped: Simple Host could not be reached. Check your connection and try again."));
      x.onload = () => {
        let b: Record<string, unknown> = {};
        try {
          b = JSON.parse(x.responseText || "{}");
        } catch {
          /* not JSON */
        }
        if (x.status >= 200 && x.status < 300) resolve(b as never);
        else reject(sentence(x.status, b as { code?: string; fix?: string }, "Could not save. Try again."));
      };
      x.send(body);
    });
  },
  async shrink(file) {
    const p = await photos();
    return p.shrink(file, { maxBytes: PHOTO_BYTES, maxDimension: PHOTO_SIDE });
  },
  slug,
  filesLimit: 200 << 20,
  siteLimit: 100 << 20,
};

type SHPhotos = { shrink: (f: File, o: { maxBytes: number; maxDimension: number }) => Promise<Shrunk> };
let loading: Promise<SHPhotos> | null = null;
function photos(): Promise<SHPhotos> {
  const w = window as unknown as { SHPhotos?: SHPhotos };
  if (w.SHPhotos) return Promise.resolve(w.SHPhotos);
  loading ??= new Promise((resolve, reject) => {
    const s = document.createElement("script");
    s.src = "/assets/photo-process.js";
    s.onload = () => (w.SHPhotos ? resolve(w.SHPhotos) : reject(new Error("Could not load the photo tools. Reload the page.")));
    s.onerror = () => {
      loading = null;
      reject(new Error("Could not load the photo tools. Check your connection and reload the page."));
    };
    document.head.appendChild(s);
  });
  return loading;
}

/** slug is a file's short name, as photo-process.js makes it: lower case, hyphens, no extension. */
export function slug(name: string): string {
  return (
    name
      .toLowerCase()
      .replace(/\.[^.]+$/, "")
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 60) || "photo"
  );
}

/** isPhoto: what is made smaller here (anything a browser or the codecs can read as a picture). */
export const isPhoto = (f: File) => /^image\//.test(f.type) || /\.(heic|heif|tiff?|avif|bmp)$/i.test(f.name);

/** extOf is a file's own extension, kept for files that are not photos. */
export const extOf = (name: string) => name.match(/\.[a-z0-9]{1,8}$/i)?.[0].toLowerCase() ?? "";

/** isArchive: a file the archive link takes. */
export const isArchive = (f: File) => /\.(zip|tar\.gz|tgz)$/i.test(f.name);

/**
 * sentShares is how far each file is through one upload, from the bytes sent
 * so far: the files go in the order given, so each is done once the bytes
 * before and including it have gone. 1 is all of it.
 */
export function sentShares(sizes: number[], sent: number, total: number): number[] {
  const all = sizes.reduce((a, n) => a + n, 0) || 1;
  // The request is a little bigger than the files (the multipart headers).
  const at = (sent / (total || 1)) * all;
  let before = 0;
  return sizes.map((n) => {
    const share = n ? Math.min(1, Math.max(0, (at - before) / n)) : at >= before ? 1 : 0;
    before += n;
    return share;
  });
}

const DepsContext = createContext<UploadDeps>(realDeps);
export const UploadDepsProvider = DepsContext.Provider;
export const useUploadDeps = () => useContext(DepsContext);
