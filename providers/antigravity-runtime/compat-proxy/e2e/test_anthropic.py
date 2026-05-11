import os
import pytest
from anthropic import Anthropic

PROXY_BASE_URL = os.getenv("PROXY_BASE_URL", "http://localhost:8080/v1")
API_KEY = os.getenv("PROXY_API_KEY", "test-secret-key")

# We override the base URL to point to our proxy's /v1/messages
client = Anthropic(
    base_url=PROXY_BASE_URL,
    api_key=API_KEY,
)

def test_anthropic_chat_completion():
    """Test standard message completion with system prompt."""
    try:
        response = client.messages.create(
            model="claude-3-5-sonnet-20240620",
            max_tokens=1024,
            system="You are a highly capable AI assistant.",
            messages=[
                {"role": "user", "content": "Write a short poem about antigravity."}
            ]
        )
        assert response.content is not None
        print("Anthropic Chat Completion Successful.")
    except Exception as e:
        pytest.fail(f"Anthropic Chat Completion failed: {e}")

def test_anthropic_tool_calling():
    """Test tool calling via Anthropic SDK."""
    try:
        response = client.messages.create(
            model="claude-3-5-sonnet-20240620",
            max_tokens=1024,
            tools=[
                {
                    "name": "get_weather",
                    "description": "Get the current weather in a given location",
                    "input_schema": {
                        "type": "object",
                        "properties": {
                            "location": {
                                "type": "string",
                                "description": "The city and state, e.g. San Francisco, CA"
                            }
                        },
                        "required": ["location"]
                    }
                }
            ],
            messages=[
                {"role": "user", "content": "What is the weather in New York?"}
            ]
        )
        assert response.content is not None
        print("Anthropic Tool Calling Response Received.")
    except Exception as e:
        pytest.fail(f"Anthropic Tool Calling failed: {e}")
