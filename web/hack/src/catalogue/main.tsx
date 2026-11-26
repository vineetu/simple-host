// Simple Hack's screen catalogue (catalogue.html; the machinery is the kit's).
// Each route family adds its own screens/<area>/catalogue.ts, picked up here
// by the glob: this file never needs editing.
import { MemoryRouter, Route, Routes } from "react-router";
import "../styles/app.css";
import { mountCatalogue, track, type Entry } from "@kit/catalogue/harness";
import { Providers } from "@/app/Providers";
import { Screen } from "@/app/Screen";
import { registry } from "@/routes/registry";

const families = import.meta.glob<{ entries: Entry[] }>("../screens/*/catalogue.ts", { eager: true });
const entries = Object.keys(families)
  .sort()
  .flatMap((f) => families[f].entries);

// A picture of one colour for every screenshot, as a site's would be.
const COLOURS = ["#c98b5f", "#6f9a8d", "#d9b86a", "#8e7cc3", "#b85c4f"];
const bytes = (path: string) =>
  track(
    new Promise<Blob>((ok) => {
      const c = document.createElement("canvas");
      c.width = 320;
      c.height = 200;
      const g = c.getContext("2d")!;
      g.fillStyle = COLOURS[path.length % COLOURS.length];
      g.fillRect(0, 0, 320, 200);
      g.fillStyle = "#fffdf7";
      g.fillRect(24, 24, 180, 28);
      g.fillRect(24, 70, 272, 100);
      c.toBlob((b) => ok(b!), "image/png");
    }),
  );

// Every entry is signed in unless it clears the key in before().
try {
  localStorage.setItem("apiKey", "catalogue");
} catch {
  /* storage blocked */
}
// Lazy screens load before the entry mounts, so the steps wait only for the API.
void Promise.all(Object.values(registry).map((d) => d.load())).then(() =>
  mountCatalogue(entries, (e, call, client) => (
  <Providers call={call} bytes={bytes} client={client}>
    <MemoryRouter initialEntries={[e.url]}>
      <Routes>
        <Route path="*" element={<Screen />} />
      </Routes>
    </MemoryRouter>
  </Providers>
  )),
);
