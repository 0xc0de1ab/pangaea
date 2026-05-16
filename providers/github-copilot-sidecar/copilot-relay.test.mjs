import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import {
  configuredModels,
  configuredModelStatus,
  ensureCopilotAuthFile,
  modelStatusFromSDKModels,
  openAIModelsFromSDKModels,
  quotaSnapshotsFromCopilotUser,
  quotaSnapshotsFromSDKQuota,
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

test("maps configured Copilot fallback models to provider metadata", () => {
  const models = configuredModels("github-copilot-default,auto,gpt-5.2");
  assert.equal(models[0].kind, "alias");
  assert.equal(models[0].label, "copilot-default");
  assert.equal(models[1].kind, "group");
  assert.deepEqual(models[1].groupMembers, ["gpt-5.2"]);

  const status = configuredModelStatus("github-copilot-default,auto,gpt-5.2");
  assert.equal(status["github-copilot-default"].kind, "alias");
  assert.equal(status.auto.kind, "group");
  assert.deepEqual(status.auto.groupMembers, ["gpt-5.2"]);
});

test("maps Copilot SDK quota snapshots to public usage payload", () => {
  const quota = quotaSnapshotsFromSDKQuota({
    quotaSnapshots: {
      chat: {
        isUnlimitedEntitlement: true,
        entitlementRequests: 0,
        usedRequests: 0,
        remainingPercentage: 100,
        resetDate: "2026-05-16T15:47:20.803Z",
      },
      premium_interactions: {
        isUnlimitedEntitlement: false,
        entitlementRequests: 300,
        usedRequests: 17,
        remainingPercentage: 94.2,
        overage: 1,
        tokenBasedBilling: false,
      },
    },
  });

  assert.deepEqual(Object.keys(quota.quotaSnapshots), ["chat", "premium_interactions"]);
  assert.equal(quota.quotaSnapshots.chat.isUnlimitedEntitlement, true);
  assert.equal(quota.quotaSnapshots.premium_interactions.entitlementRequests, 300);
  assert.equal(quota.quotaSnapshots.premium_interactions.usedRequests, 17);
  assert.equal(quota.quotaSnapshots.premium_interactions.remainingPercentage, 94.2);
});

test("maps Copilot user quota snapshots from copilot_internal user response", () => {
  const quota = quotaSnapshotsFromCopilotUser({
    quota_reset_date_utc: "2030-06-01T00:00:00.000Z",
    quota_snapshots: {
      premium_interactions: {
        entitlement: 300,
        remaining: 282,
        percent_remaining: 94,
        overage_count: 0,
        overage_permitted: false,
        unlimited: false,
      },
    },
  });

  assert.equal(quota.quotaSnapshots.premium_interactions.entitlementRequests, 300);
  assert.equal(quota.quotaSnapshots.premium_interactions.usedRequests, 18);
  assert.equal(quota.quotaSnapshots.premium_interactions.remainingPercentage, 94);
  assert.equal(quota.quotaSnapshots.premium_interactions.resetDate, "2030-06-01T00:00:00.000Z");
});

test("maps Copilot Free limited monthly quota fallback", () => {
  const quota = quotaSnapshotsFromCopilotUser({
    access_type_sku: "free_limited_copilot",
    limited_user_reset_date: "2030-06-12",
    limited_user_quotas: {
      chat: 360,
      completions: 4000,
    },
    monthly_quotas: {
      chat: 500,
      completions: 4000,
    },
  });

  assert.equal(quota.quotaSnapshots.chat.entitlementRequests, 500);
  assert.equal(quota.quotaSnapshots.chat.usedRequests, 140);
  assert.equal(quota.quotaSnapshots.chat.remainingPercentage, 72);
  assert.equal(quota.quotaSnapshots.chat.resetDate, "2030-06-12T00:00:00.000Z");
  assert.equal(quota.quotaSnapshots.completions.entitlementRequests, 4000);
  assert.equal(quota.quotaSnapshots.completions.usedRequests, 0);
});

test("restores Copilot SDK config when token fields disappear", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "pangaea-copilot-auth-"));
  const sdkConfigPath = path.join(dir, "home", ".copilot", "config.json");
  const backupPath = `${sdkConfigPath}.pangaea-auth-backup`;
  fs.mkdirSync(path.dirname(sdkConfigPath), { recursive: true });
  fs.writeFileSync(sdkConfigPath, `// User settings belong in settings.json.
// This file is managed automatically.
{
  "firstLaunchAt": "2026-05-14T15:07:22.302Z",
  "copilotTokens": {
    "https://github.com:octocat": "copilot_secret_tail"
  },
  "lastLoggedInUser": {
    "host": "https://github.com",
    "login": "octocat"
  }
}
`);

  assert.equal(ensureCopilotAuthFile({ sdkConfigPath, backupPath }), false);
  assert.match(fs.readFileSync(backupPath, "utf8"), /copilotTokens/);

  fs.writeFileSync(sdkConfigPath, `// User settings belong in settings.json.
// This file is managed automatically.
{
  "firstLaunchAt": "2026-05-14T15:07:22.302Z"
}
`);

  assert.equal(ensureCopilotAuthFile({ sdkConfigPath, backupPath }), true);
  const restored = fs.readFileSync(sdkConfigPath, "utf8");
  assert.match(restored, /copilotTokens/);
  assert.match(restored, /octocat/);
});
