# Storage retirement notices — drafts, not sent (R4-S3)

Status: draft, awaiting the owner’s wording and switch-date decisions.
The notice period is 30 days. No switch date is set by this slice.

# Notice emails (drafts, not sent)

The rules brief (R4-S3) and decision 23 ask for a separate notice for each retirement. Placeholders:
`<DATE>` is the switch date, `<DAYS>` is 30, `<SITE>` and the per-site lines are filled from
the recipient list at send time. `<RULES_GUIDE_URL>` and `<MOVING_GUIDE_URL>` are the public
guide pages (they go live with R4-S2). `<YOUR_PAGE>` is the owner's Simple Host page, as in the
idle-site emails.

Who gets which:

- **Email 1 (presets)** goes to every account that holds a site with a SQLite or key-value
  storage resource that still uses a preset. Today on Simple Host: 23 real owners, 40 sites.
  Version 1A assumes we convert their presets for them (questions.md, question 1, option A).
  Version 1B is the brief's current text, where the owner must set rules.
- **Email 2 (older saved data)** goes to every account that holds a site with shared state,
  collections or declared data. Today: 8 real owners, 13 sites. This group must act. Nothing
  converts it.
- 4 owners are on both lists and get both emails, the same day.
- Nobody on Simple Hack gets either email. It has no storage resources and no older saved data.

---

## Email 1A: presets, converted for them

Subject line

> Your site's storage switches from presets to rules on `<DATE>`

Alternatives:
- Nothing to do: we're moving your site's storage to rules
- A change to who-can-do-what settings on your Simple Host storage

```text
Hello,

On <DATE>, Simple Host stops using presets on SQLite and key-value storage. Rules replace them.

Your sites keep working, and you don't need to do anything. Before <DATE> we switch each preset below to rules that allow exactly the same things. We have tested that the rules give the same answer as the preset for every kind of visitor.

What we will switch:
<SITE> / <RESOURCE>: <what it allows today, for example "visitors can send entries, only you read them">

Afterwards, the Storage panel on your Simple Host page shows the rules where it showed the preset. You keep full access to your data. File storage keeps its presets.

If you want to write the rules yourself, you can do that any time before <DATE>. Read how rules work: <RULES_GUIDE_URL>

Questions? Just reply to this email.

Simple Host
```

## Email 1B: presets, owner sets rules (only if question 1 is answered B)

Subject line

> Set rules on your site's storage by `<DATE>`

Alternatives:
- Action needed by `<DATE>`: your site's storage presets are ending
- Presets on your Simple Host storage end on `<DATE>`

```text
Hello,

On <DATE>, Simple Host stops using presets on SQLite and key-value storage. Rules replace them.

These parts of your sites still use a preset:
<SITE> / <RESOURCE>: <what it allows today>

Your sites keep working until <DATE>. After that, visitors cannot read or save this data until you set rules. You keep full access the whole time. File storage keeps its presets.

To set rules, open the Storage panel on your Simple Host page, or ask your AI app with Simple Host connected. Read how rules work: <RULES_GUIDE_URL>

Questions? Just reply to this email.

Simple Host
```

---

## Email 2: older saved data (owner must act)

Subject line

> Older saved data on your site stops working on `<DATE>`

Alternatives:
- Action needed by `<DATE>`: older saved data on your Simple Host site
- The older ways to save data end on `<DATE>`

```text
Hello,

On <DATE>, Simple Host turns off the older ways a site saves data. These are shared state, collections and the data kinds Page info, Submissions, Personal and Shared board.

These sites of yours still use them:
<SITE>: <which ones, for example "Submissions (contact), shared state">

From <DATE>, pages on these sites can no longer read or save that data. The pages themselves stay online.

Your data is kept. You can download it from your Simple Host page with the site export: <YOUR_PAGE>
The export holds the shared state, the declared data and every collection. It does not hold other people's Personal records. Each person keeps their own Personal records in their own account export.

What to do before <DATE>: move the site to storage (SQLite, key-value or files) with rules. Your AI app can do this with Simple Host connected. How to move a site: <MOVING_GUIDE_URL>

If you no longer need these features, there is nothing to do.

Questions? Just reply to this email.

Simple Host
```

## Save response and dashboard wording

# Storage panel line (drafts)

Shown on the card of a SQLite or key-value resource that still uses a preset. These replace the text R3-S7b put on that card
(not merged yet) once `<DATE>` is set.

## During the notice period

1. **Presets end on `<DATE>`. Rules replace them and allow the same things.**
2. **This uses a preset. Presets end on `<DATE>`, and rules take their place.**

My pick: 1. It says the date and that nothing changes for visitors. If question 1 is answered
B (owners set rules), use this instead: "Presets end on `<DATE>`. Set rules before then so
visitors keep access."

## After the switch

Shown on a resource that still has no rules. The server's own repair text (RL-S1) is
"Visitors cannot use this until you set rules."

1. **Presets ended on `<DATE>`. Visitors can't use this until you set rules.**
2. **Visitors can't use this until you set rules. You still have full access.**

My pick: 2. It leads with what is wrong and reassures in five words. It matches the
server's repair text, so the panel, the AI app and the API say the same thing.

## Recipient reports

Recompute immediately before each send and reminder with `migrate-rules --list-recipients --out <file> --legacy-log <file>`. Nothing is sent by this command. The JSON report contains separate preset and older-saved-data lists, with each account address, sites, resources and current permissions. `held_by_an_event` contains resources for R4-S4, without an email recipient. No converted account is excluded; file buckets are excluded. The startup total counts all eligible resources, including those held by an event.

The runtime placeholders use email 1B until the owner decides otherwise. The editable template file is documented in [Storage and backups](../advanced/storage-and-backups.md#retirement-notices). No conversion is promised or offered to site owners. The alternate 1A above is a draft only.
