# Yuna AI Discord Bot

Yuna is a modular, multi-provider AI chatbot for Discord built to be resilient to provider rate limits and outages.

Quick start

1. Create a virtual environment and install dependencies:

```pwsh
python -m venv .venv
.\.venv\Scripts\Activate.ps1
pip install -r requirements.txt
```

2. Create a `.env` file with your Discord token and any provider keys you want to preload:

```
DISCORD_TOKEN=your_discord_bot_token
GROQ_API_KEY=your_groq_key
```

3. Run the bot:

```pwsh
py .\yuna_bot.py
```

Files of interest

- `yuna_bot.py`: Discord front-end and event handlers
- `ai_manager.py`: Provider orchestration and fallback logic
- `database.py`: Simple sqlite3 persistence (API keys, ai channels, cooldowns)
- `providers/`: Provider drivers (groq, local_stub, ...)
- `plans/`: Design docs and roadmaps

Notes

- See `plans/phase4.md` for the Phase 4 checklist and rationale for logging and deployment steps.
- Before running in production, add real API keys via `database.add_api_key()` or a simple admin command (to be implemented in Phase 5).
