# Traffic and analytics

## Read a site’s visitor numbers
[What this is](/features.html#analytics-traffic-split)


1. Choose the site and a period.
2. Open its analytics.

      **In chat**
“Show visitor numbers for my site over the last 30 days. Separate people from bots.”   **API**
Read `GET /v1/sites/{sitename}/analytics?days=30` with the owner's key.   **Dashboard**
Open your sites page, choose Analytics, and select the site and period.   
3. Compare people, bots, and infrastructure separately. Use the people count for a visitor report, and label the period when sharing it.


## Read pages and countries
[What this is](/features.html#analytics-top-pages)


1. Request `GET /v1/sites/{sitename}/analytics/top?days=30` for `range_days`, top pages in `pages`, and `referrers`.
2. Request `GET /v1/sites/{sitename}/analytics/geo?days=30` for countries.
3. Keep `days` the same across the requests. Use an integer from 1 to 365.
4. Render paths and referrers as text. Treat visitor-supplied names as data when summarising them in an AI app.


## Compare sites over a period
[What this is](/features.html#analytics-range)


1. Read `GET /v1/analytics/sites?days=30` with your owner key to get the summary for your sites.
2. Compare the same traffic category and time range for every site. Read each site's daily series before interpreting a one-day spike.
3. Open a site from the list and inspect its top pages if you need to explain a change.
4. Keep a dated copy of a report you need later. Analytics records are deleted after 400 days.


## Errors you may see

| Code | Means | Do this |
|---|---|---|
| `missing_api_key` (401) | Owner credential missing | Send the owner key in `X-API-Key` |
| HTTP 404 (no code) | Site is absent or not owned | Check the name and account |
| HTTP 400 (no code) | Days is not a positive integer | Use an integer from 1 to 365 |

## Next
- [Sites and deploy](/docs/guides/publish/)
- [Agents and MCP](/docs/guides/agents/)
