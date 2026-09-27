# AI features

**Ask about this page.** simple-host.app's features, architecture and enterprise pages carry a
small "Ask about this page" box that answers from the page's own text. It runs only when a model
backend is set (`LLM_API_KEY` and `LLM_BASE_URL`, or a provider); without one the box is not
shown. Questions are limited per address, per day across everyone, and in how many are answered
at once.

The same backend serves AI create (building a site from a description), whose settings are listed
below with one line each. A small box has no model backend, so none of these apply there.

<!-- settings:group=ai -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `AI_MAX_JOBS_PER_USER` | `3` | 1–100 builds | AI create: builds one person may run at once. |
| `AI_MAX_JOBS` | `64` | 1–1000 builds | AI create: builds running at once on the whole server. |
| `AI_JOB_TIMEOUT_MINUTES` | `8` | 1–8 minutes | AI create: how long one build may run. |
| `RATE_LIMIT_AI_IP` | `20,12s` | any (warns past 10× looser) | AI create requests per address. |
| `RATE_LIMIT_AI_USER` | `30,10s` | any (warns past 10× looser) | AI create requests per account. |
| `RATE_LIMIT_TRANSCRIBE` | `60,3s` | any (warns past 10× looser) | Voice input requests, per address and per account. |
| `ASK_ENABLED` | `on` | `on` / `off` | Whether the "Ask about this page" box is shown (it also needs a model backend). |
| `ASK_BURST` | `5` | 1–50 questions | Ask: questions one address may ask at once. |
| `ASK_EVERY_SECONDS` | `20` | 1–3600 seconds | Ask: then one more question every this many seconds, per address. |
| `ASK_DAILY_MAX` | `500` | 0–100000 questions | Ask: questions answered per day across everyone. 0 answers none. |
| `ASK_MAX_IN_FLIGHT` | `4` | 1–32 questions | Ask: questions answered at once on the whole server. |
| `ASK_MODEL` | `grok-4.7` | model name | Ask: the model the box asks, through the same backend as AI create. |
| `ASK_REASONING_EFFORT` | `none` | `none` / `low` / `medium` / `high` | Ask: how long the model thinks before answering. none answers in seconds. |
| `ASK_MAX_TOKENS` | `300` | 50–4000 tokens | Ask: longest answer, in tokens. |
| `LLM_PROVIDER` | `grok` | `custom` / `deepseek` / `grok` / `openai` / `openrouter` / `xai` | The model backend for AI create and Ask. |
| `LLM_API_KEY` | none | secret | The model backend's key. Ask and AI create run only with a backend set. **Security-sensitive.** |
| `LLM_BASE_URL` | none | text | The backend's address; wins over the provider's. |
| `LLM_MODEL` | none | text | The model AI create uses; wins over the provider's. |
| `VISION_PROVIDER` | `<LLM_PROVIDER>` | `custom` / `deepseek` / `grok` / `openai` / `openrouter` / `xai` | AI create: the backend that reads attached images. |
| `VISION_API_KEY` | none | secret | AI create: the image backend's key. **Security-sensitive.** |
| `VISION_BASE_URL` | none | text | AI create: the image backend's address. |
| `VISION_MODEL` | none | text | AI create: the image model. |
| `TRANSCRIBE_URL` | none | text | A speech-to-text service on this server for voice input. Unset: no microphone button. |
| `TRANSCRIBE_TICKET_SECRET` | none | secret | Shared with the speech service for live transcription. **Security-sensitive.** |
<!-- /settings -->

## Recipes

**Turn the box off.** `ASK_ENABLED=off`.

**Fewer questions per day.** `ASK_DAILY_MAX=100`.
