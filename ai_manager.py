# ai_manager.py
import database
from providers import groq  # Import our groq module


async def get_ai_response(conversation_history):
    """
    The main entry point for getting an AI response.
    It now takes a structured conversation history.
    """
    groq_api_key = database.get_api_key("groq")
    if not groq_api_key:
        return "Error: Groq API key is not configured."

    try:
        # The history is now passed directly to the provider
        response = await groq.generate(groq_api_key, conversation_history)
        return response
    except Exception as e:
        print(f"Error calling AI provider: {e}")
        return "Sorry, I encountered an error while trying to think."
