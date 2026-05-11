import os
import pytest
from openai import OpenAI

# Initialize client pointing to our proxy
# Default to localhost if PROXY_BASE_URL is not set
PROXY_BASE_URL = os.getenv("PROXY_BASE_URL", "http://localhost:8080/v1")
API_KEY = os.getenv("PROXY_API_KEY", "test-secret-key")

client = OpenAI(
    base_url=PROXY_BASE_URL,
    api_key=API_KEY,
)

def test_openai_chat_completion():
    """Test standard chat completion with system prompt."""
    try:
        response = client.chat.completions.create(
            model="gpt-4o",
            messages=[
                {"role": "system", "content": "You are a highly capable AI assistant."},
                {"role": "user", "content": "Write a short poem about antigravity."}
            ]
        )
        assert response.choices is not None
        assert len(response.choices) > 0
        print("OpenAI Chat Completion Successful:", response.choices[0].message.content)
    except Exception as e:
        pytest.fail(f"OpenAI Chat Completion failed: {e}")

def test_openai_tool_calling():
    """Test tool calling (functions) via OpenAI SDK."""
    tools = [
        {
            "type": "function",
            "function": {
                "name": "get_weather",
                "description": "Get the current weather in a given location",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "location": {
                            "type": "string",
                            "description": "The city and state, e.g. San Francisco, CA",
                        },
                        "unit": {"type": "string", "enum": ["celsius", "fahrenheit"]},
                    },
                    "required": ["location"],
                },
            }
        }
    ]
    try:
        response = client.chat.completions.create(
            model="gpt-4o",
            messages=[{"role": "user", "content": "What's the weather like in Boston today?"}],
            tools=tools,
            tool_choice="auto"
        )
        assert response.choices is not None
        print("OpenAI Tool Calling Response Received.")
    except Exception as e:
        pytest.fail(f"OpenAI Tool Calling failed: {e}")

def test_openai_vision():
    """Test image analysis (Vision) via OpenAI SDK."""
    try:
        response = client.chat.completions.create(
            model="gpt-4o",
            messages=[
                {
                    "role": "user",
                    "content": [
                        {"type": "text", "text": "What is in this image?"},
                        {
                            "type": "image_url",
                            "image_url": {
                                "url": "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQEASABIAAD/2wBDAP//////////////////////////////////////////////////////////////////////////////////////wgALCAABAAEBAREA/8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAGBAQABPxA=",
                                "detail": "low"
                            }
                        }
                    ]
                }
            ]
        )
        assert response.choices is not None
        print("OpenAI Vision Response Received.")
    except Exception as e:
        pytest.fail(f"OpenAI Vision failed: {e}")
