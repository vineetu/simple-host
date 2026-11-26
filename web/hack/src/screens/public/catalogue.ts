// The link pages and sign-in in every state.
import type { Entry } from "@kit/catalogue/harness";
import { entries as signin } from "./signin.catalogue";
import { entries as directoryVote } from "./directory.catalogue";

export const entries: Entry[] = [...signin, ...directoryVote];
