// Every catalogue entry at 390 and 1280 px (the kit's checks; baselines in tests/baseline).
import { catalogueTests } from "../../kit/tests/visual";
import { NOW } from "../src/catalogue/fixtures";
import { entries } from "./entries";

catalogueTests(entries, NOW);
