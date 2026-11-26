# Connector check and ChatGPT submission routine

The kit is the source of truth; update it from the tools, never from memory. Host's kit is
`plugins/openai/`, Hack's is `plugins/openai-hack/`, each led by its `SUBMISSION.md`.

## 1. Run the connector check (every few weeks, and after any tool or instruction change)

    make connector-check PRODUCT=host
    make connector-check PRODUCT=hack

`scripts/mcpeval/check.sh` builds the program and starts a local fixture (`scripts/mcpjam/fixture.sh`).
For Simple Host (the rewritten service, `next/cmd/simple-host-next`) it then runs
`scripts/mcpjam/host_checks.py` against `/mcp` (51 tools, each with annotations and an outputSchema;
a read, a publish, and an old tool name through `next/compat`), next/mcp's tool tests
(`TestEveryToolsOutputAndHints`, `TestSubmissionToolsMatchListing`) and the kit package
(`build-openai-plugin.sh`). For Simple Hack it runs MCPJam protocol, OAuth, host-compatibility and
Apps conformance; the tool annotation tests; the kit's tool list
(`TestHackSubmissionToolsMatchChatGPTListing`) and package; then the eval scenarios
(`scripts/mcpeval/run.py`), with the change against `scripts/mcpeval/baseline-hack.json` (pass
rate, tool count, tools/list tokens and bytes). It prints one PASS/FAIL table. Needs Docker or
`DB_DSN` (an empty database); Hack also needs node and network for `npx @mcpjam/cli`. GitHub runs
all of it but the evals weekly and on every push.

The evals (Simple Hack only) type each scenario of `scripts/mcpeval/hack.yaml` (a request as a person would
write it) into the `claude` CLI, headless with only the fixture's server, on a small and a mid
model, then check the result on the server. `NO_EVALS=1`, or no `claude`, skips them. Details,
traces and the report are written to `scripts/mcpeval/out/` (gitignored). One scenario:
`python3 scripts/mcpeval/run.py hack --only judge_scores --models small`.

Every check must pass. A drop in pass rate or a bigger listing needs a reason before it is merged.
After an intended change, rerun with `python3 scripts/mcpeval/run.py hack --save-baseline`
and commit the baseline file.

## 2. Update the kit from the tools

When the kit tool-list test fails, fix every tool it names in `chatgpt-app-submission.json`.
Then read `SUBMISSION.md` against the tools: tool count, starter prompts, capabilities, skills,
test cases. Change a case that names a tool or behaviour that is gone. Keep the same text in
`SUBMISSION.md` and the json files. The package step fails when they disagree.

## 3. Fill in the portal

Outputs: `dist/simple-host-openai-{plugin,skills}.zip`, `dist/simple-hack-openai-{plugin,skills}.zip`.
The portal fields are in each `SUBMISSION.md` (Listing fields, MCP tab, Reviewer sign-in, cases).
Do every TODO-OWNER line first. The owner submits in the portal.
