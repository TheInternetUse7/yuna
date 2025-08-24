"""providers/local_stub.py

A tiny local fallback provider for testing and emergency fallback.
It simply echoes the last user message with a short prefix.
"""

import logging
from typing import List, Dict, Optional

log = logging.getLogger(__name__)


async def generate_from_messages(
    api_key: Optional[str], formatted_messages: List[Dict], **kwargs
) -> str:
    """Return a simple fallback response echoing the last user message.

    This should only be used when no external providers are available.
    """
    if not formatted_messages:
        return "I have nothing to say."

    # find last user message
    last_user = None
    for msg in reversed(formatted_messages):
        if msg.get("role") == "user" and msg.get("content"):
            last_user = msg.get("content")
            break

    if not last_user:
        return "I'm here but I couldn't find what you asked."

    log.info("Local stub responding to last user message")
    return f"(Local fallback) You said: {last_user}"
