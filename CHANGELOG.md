# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `YUNA_SUMMARY_MODEL` pins the model used for memory summaries, validated
  against the chosen summary provider's model list.

### Changed

- Providers take a comma-separated `<NAME>_MODELS` list instead of a single
  `<NAME>_MODEL`. The first entry is the default and the rest are fallbacks for
  that provider, tried before the chain moves on.
- `/model` replaces `/set_preferred_model` and `/clear_preferred_model`. It has
  `set id:<model>` and `reset` subcommands, and `id` autocompletes over every
  configured model.
- A model choice is now stored per server for administrators and per user for
  DMs, rather than per server only.

## [0.1.0] - 2026-09-13

First release of the Go rewrite.

### Added

- Discord bot on Go 1.27, replacing the Python/LiteLLM implementation, with
  [Bifrost](https://github.com/maximhq/bifrost) as the model gateway.
- Ordered provider chain with automatic fallback.
- Slash commands: `/chat`, `/set_ai_channel`, `/remove_ai_channel`,
  `/list_ai_channels`, `/provider_status`, `/set_preferred_model`,
  `/clear_preferred_model`, `/remember`, `/memory` and `/forget`.
- AI channels, where every message is answered, plus mention and reply-to-bot
  triggers everywhere else.
- One generation per channel at a time, with bursts coalesced to the newest
  message and a panicking turn contained to itself.
- Persistent memory in SQLite: per-scope facts, rolling channel summaries and a
  configurable history window.
- Numbered, append-only SQL migrations under `internal/store/migrations`.
- `--version` and `-check` flags; `-check` validates config, opens the database
  and initialises the AI client without connecting to Discord.
- Multi-stage Dockerfile and `docker-compose.yml` publishing to ghcr.io, with
  documented backup and restore steps for the data volume.

[Unreleased]: https://github.com/TheInternetUse7/yuna/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/TheInternetUse7/yuna/releases/tag/v0.1.0
