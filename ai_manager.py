# ai_manager.py
import logging
from typing import List, Dict, Optional

import database
from providers import groq, local_stub

log = logging.getLogger(__name__)


# Provider priority list. Each entry must have a name and a module.
# The manager will try them in order and skip ones that are on cooldown.
PROVIDERS = [
    {"name": "groq", "module": groq, "model": "llama3-8b-8192"},
    # Future providers can be added here: gemini, cohere, openrouter, etc.
    {"name": "local_stub", "module": local_stub},
]


# Default cooldown in seconds when a provider hits a rate limit
DEFAULT_COOLDOWN_SECONDS = 60


async def get_ai_response(conversation_history: List[Dict]) -> str:
    """Attempt to get an AI response from the configured providers.

    The function formats the provided conversation history for providers,
    then iterates the `PROVIDERS` list, skipping providers with no key
    (except local_stub) or ones currently on cooldown. On rate-limit
    detection the provider is placed on cooldown and the next provider
    is attempted.

    Returns the provider text response or a friendly error message.
    """
    # Build a simple system prompt and format messages for providers.
    system_prompt = "You are Yuna, a helpful AI assistant on Discord."
    formatted_messages: List[Dict] = [{"role": "system", "content": system_prompt}]

    for msg in conversation_history:
        role = msg.get("role", "user")
        content = msg.get("content", "")
        # Only pass role and content to providers
        formatted_messages.append({"role": role, "content": content})

    # Try providers in order
    for provider in PROVIDERS:
        name = provider["name"]

        # Skip provider if on cooldown
        if database.is_on_cooldown(name):
            log.info("Skipping %s: on cooldown", name)
            continue

        api_key: Optional[str] = None
        if name != "local_stub":
            api_key = database.get_api_key(name)
            if not api_key:
                log.info("No API key for provider %s; skipping.", name)
                continue

        try:
            log.info("Trying provider: %s", name)
            # Call the provider's generate method. Providers should implement
            # `generate_from_messages(api_key, formatted_messages)` returning a string.
            if hasattr(provider["module"], "generate_from_messages"):
                result = await provider["module"].generate_from_messages(
                    api_key, formatted_messages
                )
            else:
                result = await provider["module"].generate(api_key, formatted_messages)

            log.info("Provider %s succeeded.", name)
            return result

        except Exception as e:
            err_text = str(e)
            log.warning("Provider %s failed: %s", name, err_text)
            # Heuristic: if the error message or repr suggests rate-limiting, set cooldown
            lowered = err_text.lower()
            if (
                "rate limit" in lowered
                or "429" in lowered
                or "too many requests" in lowered
            ):
                log.warning(
                    "Rate limit detected for %s — setting cooldown %ss",
                    name,
                    DEFAULT_COOLDOWN_SECONDS,
                )
                database.set_cooldown(name, DEFAULT_COOLDOWN_SECONDS)
                continue
            # Non-rate-limit failures: move to the next provider
            continue

    # If we get here, all providers failed
    log.error("All providers failed to return a response")
    return (
        "I'm sorry, all AI providers are currently unavailable. Please try again later."
    )
