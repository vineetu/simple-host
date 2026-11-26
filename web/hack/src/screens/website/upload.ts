// Sending bytes (a zip, a screenshot) the way the old api.js apiBytes did:
// the key as X-API-Key, X-SH-CSRF: 1, the file as the body. A refusal is the
// same ApiError a JSON call gives; a refused key signs the person out.
import { ApiError } from "@kit/api/call";
import { apiKey, signedOut } from "@/lib/session";

export async function putBytes<T>(method: "PUT" | "POST", path: string, body: Blob, type: string): Promise<T> {
  const headers: Record<string, string> = { "X-SH-CSRF": "1" };
  const k = apiKey();
  if (k) headers["X-API-Key"] = k;
  if (type) headers["Content-Type"] = type;
  let res: Response;
  try {
    res = await fetch(path, { method, headers, body });
  } catch {
    throw new ApiError(0, "offline", "Cannot reach the server.");
  }
  const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
  if (res.status === 401) {
    signedOut();
    throw new ApiError(401, "signed_out", "You are signed out.");
  }
  if (!res.ok) {
    const code = typeof data.code === "string" && data.code ? data.code : "error";
    const msg = res.status >= 500 ? "Something went wrong. Try again." : typeof data.error === "string" && data.error ? data.error : "Something went wrong.";
    throw new ApiError(res.status, code, msg, data);
  }
  return data as T;
}

/** save hands the browser a file to keep. */
export function save(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
