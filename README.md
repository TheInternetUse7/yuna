# Yuna

A Discord chatbot with memory, written in Go and backed by
[Bifrost](https://github.com/maximhq/bifrost) for provider routing and failover.

## Requirements

- Go 1.27 or newer to build from source (or Docker).
- A Discord application with a bot user.
- At least one provider API key.

### Discord setup

1. Create an application at <https://discord.com/developers/applications>.
2. Under **Bot**, copy the token into `DISCORD_TOKEN`.
3. Under **Bot -> Privileged Gateway Intents**, enable **Message Content** and
   **Server Members**. Both are required: without the first the bot receives
   empty message content, and without the second it cannot resolve member
   nicknames.
4. Invite the bot with the `bot` and `applications.commands` scopes.

## Configuration

Copy `.env.example` to `.env` and fill it in. Every setting is documented in
that file; the essentials are:

| Variable                       | Required    | Default       | Purpose                                                       |
| ------------------------------ | ----------- | ------------- | ------------------------------------------------------------- |
| `DISCORD_TOKEN`                | yes         | -             | Bot token                                                     |
| `DISCORD_GUILD_ID`             | no          | global        | Register commands on one server (instant) instead of globally |
| `YUNA_PROVIDERS`               | yes         | -             | Ordered chain: first is primary, the rest are fallbacks       |
| `<NAME>_MODELS`                | yes         | -             | Comma-separated model IDs, tried in order; first is default   |
| `<NAME>_API_KEY`               | built-ins   | -             | API key for that provider                                     |
| `<NAME>_BASE_URL`              | custom only | -             | OpenAI-compatible host; omit `/v1`, Bifrost adds the path     |
| `<NAME>_TOOLS`                 | no          | catalog       | Whether the `remember` tool is offered to this provider       |
| `YUNA_MEMORY_ENABLED`          | no          | `true`        | Master switch for summaries and facts                         |
| `YUNA_HISTORY_WINDOW`          | no          | `15`          | Messages sent as conversation context                         |
| `YUNA_SUMMARY_EVERY`           | no          | `25`          | New messages before a summary refresh                         |
| `YUNA_SUMMARY_PROVIDER`        | no          | last in chain | Provider used for summaries                                   |
| `YUNA_SUMMARY_MODEL`           | no          | provider's first model | Model used for summaries                             |
| `YUNA_FACTS_PER_USER_LIMIT`    | no          | `50`          | Stored facts per person per scope                             |
| `YUNA_FACTS_INJECT_LIMIT`      | no          | `20`          | Facts injected into one prompt                                |
| `YUNA_MAX_RETRIES`             | no          | `2`           | Retries per provider before failing over                      |
| `YUNA_REQUEST_TIMEOUT_SECONDS` | no          | `30`          | Per-request timeout                                           |
| `YUNA_SYSTEM_PROMPT`           | no          | built-in      | Replaces the persona                                          |
| `YUNA_DB_PATH`                 | no          | `yuna.db`     | `/data/yuna.db` in the container                              |
| `YUNA_LOG_FILE`                | no          | `yuna.log`    | `/data/yuna.log` in the container                             |
| `YUNA_DEBUG`                   | no          | `false`       | `1` enables debug logging                                     |

### Choosing providers

Each provider takes a comma-separated list of models, tried in the order given,
and `YUNA_PROVIDERS` orders the providers themselves. A request starts at the
first model of the first provider, then works through that provider's remaining
models, then the next provider:

```
YUNA_PROVIDERS=gemini,groq,openrouter
GEMINI_MODELS=gemini-2.5-flash,gemini-2.5-pro
GEMINI_API_KEY=...
GROQ_MODELS=llama-3.3-70b-versatile,llama-3.1-8b-instant
GROQ_API_KEY=...
OPENROUTER_MODELS=anthropic/claude-3.5-sonnet
OPENROUTER_API_KEY=...
```

That ladder is what keeps a rate-limited or retired model from taking the whole
vendor out of rotation.

### Choosing a model from Discord

`/model set id:<model>` sets which model answers — for the whole server when an
administrator runs it, and for the caller's own DMs otherwise. `/model reset`
clears the choice. The `id` option autocompletes over the raw model IDs from the
configured lists; nothing outside them can be selected.

Built-in names: `gemini`, `openai`, `anthropic`, `groq`, `cerebras`,
`openrouter`, `cohere`, `mistral`, `deepseek`, `xai`, `perplexity`, `nebius`,
`fireworks`, `huggingface`, `replicate`, `sarvam`, `parasail`, `wafer`.

### Self-hosted and OpenAI-compatible endpoints

Any other name becomes a custom provider as soon as you give it a base URL. This
covers Ollama, vLLM, LM Studio, llama.cpp, and gateways:

```
YUNA_PROVIDERS=ollama,gemini
OLLAMA_MODELS=llama3.1:8b
OLLAMA_BASE_URL=http://host.docker.internal:11434
GEMINI_MODELS=gemini-2.5-flash
GEMINI_API_KEY=...
```

No API key is needed for a custom endpoint; omit `<NAME>_API_KEY` and the
provider is registered as keyless. A private or loopback base URL is allowed
automatically, while public hosts still go through the normal path.

Because custom providers keep their own name, two OpenAI-compatible endpoints
can sit in the same chain without colliding.

## Commands

| Command                                             | Who      | Reply     | What it does                                |
| --------------------------------------------------- | -------- | --------- | ------------------------------------------- |
| `/chat prompt:<text>`                               | everyone | public    | Ask a one-off question                      |
| `/remember fact:<text>`                             | everyone | ephemeral | Ask Yuna to remember something about you    |
| `/memory`                                           | everyone | ephemeral | Show what she remembers about you here      |
| `/forget [all] [include_history]`                   | everyone | ephemeral | Delete what she remembers about you         |
| `/set_ai_channel`                                   | admin    | ephemeral | Reply to every message in this channel      |
| `/remove_ai_channel`                                | admin    | ephemeral | Stop replying to every message              |
| `/list_ai_channels`                                 | admin    | ephemeral | List this server's AI channels              |
| `/provider_status`                                  | admin    | ephemeral | Show the effective chain and settings       |
| `/model set id:<model>`                             | admin in a server, everyone in DMs | ephemeral | Choose which model answers |
| `/model reset`                                      | admin in a server, everyone in DMs | ephemeral | Return to the configured order |

`/memory` and `/forget` always act on the person who ran them, in the scope they
ran them in. Those replies are ephemeral.

`/model` needs Administrator to change a server's model, but anyone can set the
model for their own DMs. The `id` option autocompletes over the configured
models, shown as `provider · model-id`.

## Memory and privacy

Memory has three parts:

1. **Conversation history**, per channel.
2. **Rolling summaries**, per channel, refreshed in the background once enough
   new messages accumulate.
3. **Facts about people**, per scope.

### Scopes

A fact belongs to exactly one scope and one person:

- In a server, the scope is that server. Facts follow you between its channels.
- A DM is its own scope, private to you.
- Facts never cross between servers, and never between a server and a DM.

### What gets stored

Only messages Yuna has a reason to remember are saved:

- any message in an AI channel,
- any DM,
- any message that mentions her,
- any reply to something she wrote,
- everything she writes.

`/forget` is the opt-out: by default it removes your facts in the current scope,
`all: true` removes them everywhere, and `include_history: true` also deletes
your stored messages from that channel.

Nothing expires on its own.

## Running

### Locally

```bash
go run . -check   # validate .env, open and migrate the database, then exit
go run .
```

`-check` is the quick way to test a configuration change: it loads `.env`,
validates every provider, opens the SQLite database and initialises the AI
client, then exits without connecting to Discord.

`.env` is read from the working directory, so run these from the repository
root. Variables already present in the environment win over the file.

### Docker

```bash
docker build -t yuna:test .
docker run --rm --env-file .env -v yuna-data:/data yuna:test
```

### On the server

`docker-compose.yml` pulls the published image:

```bash
docker compose pull
docker compose up -d
docker compose logs -f
```

The image is `ghcr.io/theinternetuse7/yuna`.

### Backups

The database lives on the `yuna-data` named volume.

SQLite runs in WAL mode. Stop the container first: on a
clean shutdown SQLite checkpoints the WAL back into `yuna.db`.

```bash
docker compose stop
mkdir -p backups
docker compose cp yuna:/data/yuna.db "backups/yuna-$(date +%F).db"
docker compose start
```

`docker compose cp` needs Compose v2.14 or newer.

To restore, stop the bot, copy the file back, and fix ownership.

```bash
docker compose stop
docker compose cp backups/yuna-2026-09-13.db yuna:/data/yuna.db
docker compose run --rm --user root --entrypoint chown yuna -R 10001:10001 /data
docker compose start
```

`docker compose up`, `pull`, rebuilds and `docker compose down` all leave an
existing named volume alone, so your data survives them. Only
`docker compose down -v` or `docker volume rm yuna-data` delete it.

## Health and logs

Logs go to stdout (for `docker logs`) and to `YUNA_LOG_FILE`, which rotates at
10 MiB with three compressed backups kept.

## Development

```bash
go mod tidy
go vet ./...
go test ./...
go build ./...
```

Layout:

```
main.go                  wiring: config -> logger -> store -> ai -> bot, and shutdown
internal/applog/         leveled console + rotating file logging
internal/config/         environment loading, provider catalog, validation
internal/store/          SQLite, with ordered migrations
internal/store/migrations/
internal/ai/             Bifrost account, chat with fallbacks and the tool loop
internal/memory/         scopes, prompt assembly, facts, summaries
internal/bot/            Discord session, triggers, commands, chunking
```

### Database migrations

Migrations are plain SQL files in `internal/store/migrations`, named
`<number>_<description>.sql` and applied in ascending order inside a transaction.
