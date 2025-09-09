import logging
from typing import Dict, List
import litellm
import database

log = logging.getLogger(__name__)

# Provider priority list based on artificial analysis
# Ordered by intelligence score (highest to lowest)
PROVIDER_PRIORITY = [
    {
        "name": "gemini",
        "model": "gemini/gemini-2.5-flash",
        "score": 59.6,
        "description": "Google AI Studio - Primary provider",
    },
    {
        "name": "openrouter",
        "model": "openrouter/openai/gpt-oss-120b",
        "score": 57.9,
        "description": "OpenRouter - High-performance fallback",
    },
    {
        "name": "cerebras",
        "model": "cerebras/qwen-3-235b-a22b",
        "score": 45.3,
        "description": "Cerebras - Fast inference",
    },
    {
        "name": "groq",
        "model": "groq/llama4-maverick-17b-128e-instruct",
        "score": 35.8,
        "description": "Groq - Ultra-fast inference",
    },
    {
        "name": "cohere",
        "model": "cohere/command-a",
        "score": 28.1,
        "description": "Cohere - Reliable fallback",
    },
    {
        "name": "together_ai",
        "model": "together_ai/meta-llama/Llama-3.3-70B-Instruct-hf",
        "score": 27.9,
        "description": "Together AI - Final fallback",
    },
]

# Configuration constants
DEFAULT_COOLDOWN_SECONDS = 300  # 5 minutes cooldown for rate limits
GENERAL_ERROR_COOLDOWN_SECONDS = 60  # 1 minute cooldown for general errors


async def get_ai_response(
    conversation_history: List[Dict], guild_id: int = None
) -> Dict[str, str]:
    """
    Orchestrates fetching an AI response with resilient multi-provider fallback.
    Handles rate limits, cooldowns, and provider failures gracefully.

    Args:
        conversation_history: List of message dictionaries
        guild_id: Optional guild ID for preferred model override

    Returns:
        Dict with 'content' (response text) and 'model' (provider/model used)
    """
    system_prompt = "You are Yuna, a helpful AI assistant on Discord. Be conversational and friendly."

    formatted_messages: List[Dict] = [{"role": "system", "content": system_prompt}]

    # Format conversation history for the AI model
    for message in conversation_history:
        if message["role"] == "user":
            author = message["author_details"]
            # Format the user's message with their identity details
            content = (
                f"User '{author['username']}' (Display Name: '{author['display_name']}', "
                f"Nickname: '{author['server_nickname']}') says: {message['content']}"
            )
            formatted_messages.append({"role": "user", "content": content})
        else:  # role == 'assistant'
            formatted_messages.append(
                {"role": "assistant", "content": message["content"]}
            )

    # Check for preferred model override
    preferred_provider = None
    if guild_id:
        preferred_model_info = database.get_preferred_model(guild_id)
        if preferred_model_info:
            # Find the provider that matches the preferred model
            for provider in PROVIDER_PRIORITY:
                if provider["name"] == preferred_model_info["provider_name"]:
                    # Check if preferred provider is available (not on cooldown)
                    if not database.is_on_cooldown(provider["name"]):
                        preferred_provider = provider
                        log.info(f"Using preferred model override: {provider['model']}")
                        break
                    else:
                        log.warning(
                            f"Preferred provider '{provider['name']}' is on cooldown, falling back to priority list"
                        )
                        break

    # If we have a preferred provider that's available, try it first
    if preferred_provider:
        available_providers = [preferred_provider]
        # Add other providers as fallbacks (excluding the preferred one to avoid duplication)
        for provider in PROVIDER_PRIORITY:
            if provider["name"] != preferred_provider[
                "name"
            ] and not database.is_on_cooldown(provider["name"]):
                available_providers.append(provider)
    else:
        # Use normal priority order
        available_providers = []
        for provider in PROVIDER_PRIORITY:
            service_name = provider["name"]

            # Skip providers that are on cooldown
            if database.is_on_cooldown(service_name):
                log.info(f"Provider '{service_name}' is on cooldown, skipping")
                continue

            available_providers.append(provider)

    if not available_providers:
        log.warning("All providers are on cooldown!")
        return {
            "content": "I'm temporarily overwhelmed across all my thinking-cores. Please try again in a few minutes.",
            "model": "system/error",
        }

    log.info(
        f"Attempting AI response with {len(available_providers)} available providers"
    )

    # Try each available provider
    for i, provider in enumerate(available_providers):
        service_name = provider["name"]
        model = provider["model"]

        try:
            log.info(
                f"Attempting provider {i+1}/{len(available_providers)}: {service_name} ({model})"
            )

            response = await litellm.acompletion(
                model=model,
                messages=formatted_messages,
                timeout=30,  # 30 second timeout per provider
            )

            response_content = response.choices[0].message.content
            log.info(f"✅ Success with {service_name} - {provider['description']}")
            return {"content": response_content, "model": model}

        except litellm.RateLimitError as e:
            log.warning(f"⚠️ Rate limit hit for {service_name}: {e}")
            database.set_cooldown(service_name, DEFAULT_COOLDOWN_SECONDS)
            continue  # Try next provider

        except litellm.AuthenticationError as e:
            log.error(f"🔑 Authentication error for {service_name}: {e}")
            database.set_cooldown(service_name, GENERAL_ERROR_COOLDOWN_SECONDS)
            continue  # Try next provider

        except litellm.BadRequestError as e:
            log.error(f"❌ Bad request for {service_name}: {e}")
            # Don't cooldown for bad requests, might be model-specific
            continue  # Try next provider

        except Exception as e:
            log.error(f"💥 Unexpected error with {service_name}: {e}", exc_info=True)
            database.set_cooldown(service_name, GENERAL_ERROR_COOLDOWN_SECONDS)
            continue  # Try next provider

    # If we get here, all providers failed
    log.error("🚨 All available providers failed!")
    return {
        "content": "I'm experiencing technical difficulties across all my thinking-cores. Please try again in a moment.",
        "model": "system/error",
    }


def get_provider_status() -> Dict:
    """Returns the current status of all providers including cooldown information."""
    cooldown_status = database.get_cooldown_status()

    provider_status = {
        "total_providers": len(PROVIDER_PRIORITY),
        "available_providers": 0,
        "providers": [],
    }

    for provider in PROVIDER_PRIORITY:
        service_name = provider["name"]
        is_cooled = database.is_on_cooldown(service_name)

        status_info = {
            "name": service_name,
            "model": provider["model"],
            "score": provider["score"],
            "description": provider["description"],
            "available": not is_cooled,
            "cooldown_info": cooldown_status.get(
                service_name, {"on_cooldown": False, "remaining_seconds": 0}
            ),
        }

        if not is_cooled:
            provider_status["available_providers"] += 1

        provider_status["providers"].append(status_info)

    return provider_status


def get_provider_summary() -> str:
    """Returns a human-readable summary of provider status."""
    status = get_provider_status()
    available = status["available_providers"]
    total = status["total_providers"]

    summary = f"🤖 **Provider Status**: {available}/{total} available\n\n"

    for provider in status["providers"]:
        name = provider["name"].title()
        model = provider["model"]
        score = provider["score"]

        if provider["available"]:
            summary += f"✅ **{name}** - `{model}` (Score: {score})\n"
        else:
            remaining = provider["cooldown_info"]["remaining_seconds"]
            minutes = remaining // 60
            seconds = remaining % 60
            summary += f"⏳ **{name}** - Cooldown ({minutes}m {seconds}s remaining)\n"

    return summary
