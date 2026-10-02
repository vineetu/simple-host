# Simple Hack package preparation — 0.2.0 draft

The first proposed OpenAI upload is `simple-hack-skills-only-0.2.0.zip`. It contains exactly five skills: `run-hackathon`, `join-hackathon`, `judge-hackathon`, `website-deploy`, and `website-deploy-builder`. It has no `mcp.json`, app binding or MCP server declaration. The last two skills are copied from the same maintained Simple Host source, with a short conditional Simple Hack note. Self-hosting remains an optional `run-hackathon` reference rather than a sixth primary skill. `python3 hack-toolkit/build.py` makes deterministic ZIPs from the maintained files in `simple-host-website/skills/`.

## OpenAI skills-only submission

- Listing: **Simple Hack**; subtitle **Run a hackathon with AI** (23 characters); category **Productivity**; intended verified individual **Vineet Sriram**. Confirm the identity selected in the Developer Portal before upload.
- Listing URLs: home `https://simple-hack.app/`, support `https://simple-hack.app/support`, privacy `https://simple-hack.app/privacy.html`, terms `https://simple-hack.app/terms`. These public pages were checked on 2026-10-01. Recheck their content and access after the expanded features deploy and before upload. The policies must cover the submitted behavior.
- Release notes are in `plugin.json`. Country targeting is intentionally absent pending the publisher's choice. The hosted service is free and does not take payments (`commerce: false`); confirm if that changes. The two contained PNG icons are 512 and 256 pixels square.
- A skills-only plugin needs no MCP review cases, recorded app demo, or reviewer credentials. Its skills prefer Simple Hack connector tools when available and otherwise use the person's simple-hack.app key through REST. The connector is a separate optional connection, not bundled in this primary ZIP.
- Package preparation is separate from portal setup. After the expanded connector and event-page features are deployed and checked, upload this ZIP under the intended verified publisher, inspect the imported listing and five skills, run the portal checks, complete developer/domain verification, required scans and legal attestations, then submit for review when the owner authorises it. Upload, submission and publication are separate actions. No portal action or attestation is claimed here.

## Other local packages

`simple-hack-skills-0.2.0.zip` is the five skills for direct installation. `simple-hack-claude-0.2.0.zip` contains the same five skills plus a Claude MCP manifest. The public [Claude Code marketplace source](https://github.com/vineetu/simple-hack-plugin) currently serves the prior release; this local ZIP is not a new marketplace publication or an Anthropic directory approval.

`simple-hack-openai-0.2.0.zip` is a secondary full MCP draft. It carries the five skills, the verified `https://simple-hack.app/mcp` endpoint, and five positive and three negative draft cases. Before its own initial public review it needs a real recording in ChatGPT or Codex, a reviewer-accessible URL, secure reviewer credentials, host-run cases, and portal setup. The dedicated reviewer account and sample events prepared for the previous package remain private and must be rechecked against the expanded tool set. Do not put credentials, keys, codes or private fixture files in any ZIP. Case outcomes are not recorded as tested through the intended host.

## Privacy data map for policy review

The hosted product handles account email/name and sign-in, organiser event details and contact email, participant applications and teams, project entries and files, voting identity, judge assignments/scores/comments, OAuth grants, mail records, exports and usage logs. Organisers see their event management data and exports; judges see eligible project material and their own scores; participants see their own team and published results; public event and published project pages are public. Recheck the live privacy and terms pages against the final expanded behavior. This is engineering input, not a legal attestation.
