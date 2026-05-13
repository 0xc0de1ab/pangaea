import test from "node:test";
import assert from "node:assert/strict";

import {
  modelStatusFromSDKModels,
  openAIModelsFromSDKModels,
} from "./copilot-relay.mjs";

test("maps Copilot SDK models to OpenAI model list", () => {
  const models = openAIModelsFromSDKModels([
    {
      id: "gpt-5",
      name: "GPT-5",
      capabilities: { supports: { reasoningEffort: true }, limits: { max_context_window_tokens: 272000 } },
    },
    {
      id: "claude-sonnet-4.5",
      name: "Claude Sonnet 4.5",
      capabilities: { supports: {}, limits: { max_context_window_tokens: 200000 } },
    },
    {
      id: "disabled-model",
      name: "Disabled",
      policy: { state: "disabled" },
    },
    { id: "gpt-5", name: "Duplicate" },
  ]);

  assert.deepEqual(models.map((model) => model.id), ["gpt-5", "claude-sonnet-4.5"]);
  assert.equal(models[0].owned_by, "github-copilot");
});

test("maps Copilot SDK model metadata to Pangaea status details", () => {
  const status = modelStatusFromSDKModels([
    {
      id: "auto",
      name: "Auto",
      capabilities: {
        supports: {},
        limits: {},
      },
    },
    {
      id: "gpt-5",
      name: "GPT-5",
      supportedReasoningEfforts: ["low", "medium", "high"],
      defaultReasoningEffort: "medium",
      capabilities: {
        supports: { vision: true, reasoningEffort: true },
        limits: { max_context_window_tokens: 272000 },
      },
    },
  ]);

  assert.equal(status.auto.kind, "group");
  assert.deepEqual(status.auto.groupMembers, ["gpt-5"]);
  assert.equal(status["gpt-5"].label, "GPT-5");
  assert.equal(status["gpt-5"].maxTokens, 272000);
  assert.equal(status["gpt-5"].supportsImages, true);
  assert.deepEqual(status["gpt-5"].supportedReasoningEfforts, ["low", "medium", "high"]);
  assert.equal(status["gpt-5"].defaultReasoningEffort, "medium");
});
