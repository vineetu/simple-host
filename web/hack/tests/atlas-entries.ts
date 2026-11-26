// Every catalogue state (and every server-written page) as JSON, for the UX
// atlas (docs/ux-atlas/build.py). Run from web/:
//   npx vite-node -c hack/vite.config.ts hack/tests/atlas-entries.ts
import { entries } from "./entries";
import { pageEntries } from "../src/pages/catalogue";

const states = [...entries.map((e) => ({ id: e.id, title: e.title })), ...pageEntries.map((e) => ({ id: e.id, title: e.title, page: true }))];
process.stdout.write(JSON.stringify(states) + "\n");
