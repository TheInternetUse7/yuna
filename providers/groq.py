# providers/groq.py
import aiohttp

API_URL = "https://api.groq.com/openai/v1/chat/completions"


async def generate(api_key, conversation_history):
    """
    Generates a response from the Groq API.
    conversation_history is expected to be a list of dicts,
    but for Phase 1, we'll just handle a single prompt.
    """
    headers = {"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"}

    # The conversation_history is a list of internal message objects that may
    # contain extra metadata (e.g. `author_details`). The API expects a simple
    # list of messages with `role` and `content` only.
    cleaned_messages = []
    for msg in conversation_history:
        # Defensive: ensure msg is a dict and has the fields we need
        if not isinstance(msg, dict):
            continue
        role = msg.get("role", "user")
        content = msg.get("content", "")
        # Skip empty content
        if content is None:
            content = ""
        cleaned_messages.append({"role": role, "content": content})

    payload = {
        "model": "llama3-8b-8192",
        "messages": cleaned_messages,
    }

    async with aiohttp.ClientSession() as session:
        async with session.post(API_URL, headers=headers, json=payload) as response:
            if response.status == 200:
                data = await response.json()
                return data["choices"][0]["message"]["content"]
            else:
                # Basic error handling for now
                error_text = await response.text()
                raise Exception(f"Groq API Error: {response.status} - {error_text}")
