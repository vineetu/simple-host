# AI features

AI create builds a site from a description through the Grok sidecar. Its settings are listed
below. A small box has no model backend by default.

<!-- settings:group=ai -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `AI_MAX_JOBS_PER_USER` | `3` | 1–100 builds | AI create: builds one person may run at once. |
| `AI_MAX_JOBS` | `64` | 1–1000 builds | AI create: builds running at once on the whole server. |
| `AI_JOB_TIMEOUT_MINUTES` | `8` | 1–8 minutes | AI create: how long one build may run. |
| `RATE_LIMIT_AI_IP` | `20,12s` | any (warns past 10× looser) | AI create requests per address. |
| `RATE_LIMIT_AI_USER` | `30,10s` | any (warns past 10× looser) | AI create requests per account. |
| `LLM_PROVIDER` | `grok` | `custom` / `deepseek` / `grok` / `openai` / `openrouter` / `xai` | The model backend for AI create. |
| `LLM_API_KEY` | none | secret | The model backend's key. AI create runs only with a backend set. **Security-sensitive.** |
| `LLM_BASE_URL` | none | text | The backend's address; wins over the provider's. |
| `LLM_MODEL` | none | text | The model AI create uses; wins over the provider's. |
| `VISION_PROVIDER` | `<LLM_PROVIDER>` | `custom` / `deepseek` / `grok` / `openai` / `openrouter` / `xai` | AI create: the backend that reads attached images. |
| `VISION_API_KEY` | none | secret | AI create: the image backend's key. **Security-sensitive.** |
| `VISION_BASE_URL` | none | text | AI create: the image backend's address. |
| `VISION_MODEL` | none | text | AI create: the image model. |
<!-- /settings -->

## Recipes

**Limit simultaneous builds.** Set `AI_MAX_JOBS_PER_USER` and `AI_MAX_JOBS`.
