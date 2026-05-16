#!/usr/bin/env node
import http from "node:http";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";

function loadCopilotSDK() {
  const localRequire = createRequire(import.meta.url);
  try {
    return localRequire("@github/copilot-sdk");
  } catch (firstErr) {
    const candidates = [
      process.env.COPILOT_RELAY_NODE_MODULES,
      ...(process.env.NODE_PATH || "").split(path.delimiter),
      "/opt/copilot-relay/node_modules",
    ].filter(Boolean);
    for (const candidate of candidates) {
      try {
        return createRequire(path.join(candidate, "pangaea-relay.cjs"))("@github/copilot-sdk");
      } catch {
        // Try the next configured package root.
      }
    }
    throw firstErr;
  }
}

let copilotSDK;

function getCopilotSDK() {
  if (!copilotSDK) {
    copilotSDK = loadCopilotSDK();
  }
  return copilotSDK;
}

function newCopilotClient(options) {
  const { CopilotClient } = getCopilotSDK();
  return new CopilotClient(options);
}

function stripWholeLineJSONComments(raw) {
  return String(raw || "")
    .split(/\n/)
    .filter((line) => !line.trim().startsWith("//"))
    .join("\n")
    .trim();
}

function readCopilotConfig(filePath) {
  try {
    const raw = fs.readFileSync(filePath, "utf8");
    const body = stripWholeLineJSONComments(raw);
    return body ? JSON.parse(body) : {};
  } catch {
    return null;
  }
}

function hasCopilotToken(config) {
  if (!config || typeof config !== "object") return false;
  for (const key of ["copilotTokens", "copilotToken"]) {
    const tokens = config[key];
    if (!tokens || typeof tokens !== "object") continue;
    for (const value of Object.values(tokens)) {
      if (typeof value === "string" && value.trim()) return true;
    }
  }
  return false;
}

function copyCopilotConfig(src, dst) {
  fs.mkdirSync(path.dirname(dst), { recursive: true, mode: 0o700 });
  fs.copyFileSync(src, dst);
  try {
    fs.chmodSync(dst, 0o600);
  } catch {
    // chmod may fail on some bind mounts; the source file is still usable.
  }
}

function defaultCopilotConfigPath() {
  return path.join(process.env.HOME || os.homedir(), ".copilot", "config.json");
}

export function ensureCopilotAuthFile(options = {}) {
  const sdkConfigPath = options.sdkConfigPath || process.env.PANGAEA_COPILOT_CONFIG_PATH || defaultCopilotConfigPath();
  const sourcePath = options.sourcePath || process.env.PANGAEA_COPILOT_AUTH_SOURCE_PATH || process.env.PANGAEA_AUTH_PATH || sdkConfigPath;
  const backupPath = options.backupPath || process.env.PANGAEA_COPILOT_AUTH_BACKUP_PATH || `${sdkConfigPath}.pangaea-auth-backup`;

  const sdkConfig = readCopilotConfig(sdkConfigPath);
  if (hasCopilotToken(sdkConfig)) {
    if (backupPath && backupPath !== sdkConfigPath) {
      copyCopilotConfig(sdkConfigPath, backupPath);
    }
    return false;
  }

  if (sourcePath && sourcePath !== sdkConfigPath && hasCopilotToken(readCopilotConfig(sourcePath))) {
    copyCopilotConfig(sourcePath, sdkConfigPath);
    if (backupPath && backupPath !== sourcePath && backupPath !== sdkConfigPath) {
      copyCopilotConfig(sourcePath, backupPath);
    }
    return true;
  }

  if (backupPath && backupPath !== sdkConfigPath && hasCopilotToken(readCopilotConfig(backupPath))) {
    copyCopilotConfig(backupPath, sdkConfigPath);
    if (sourcePath && sourcePath !== sdkConfigPath && sourcePath !== backupPath) {
      copyCopilotConfig(backupPath, sourcePath);
    }
    return true;
  }

  return false;
}

function prepareCopilotAuth() {
  if (ensureCopilotAuthFile()) {
    authStatusCache = { expiresAt: 0, value: null };
    modelsCache = { expiresAt: 0, value: null };
  }
}

function approveAllPermissions(...args) {
  const { approveAll } = getCopilotSDK();
  return approveAll(...args);
}

function parseListen(argv) {
  const idx = argv.indexOf("--listen");
  const raw = idx >= 0 ? argv[idx + 1] : process.env.COPILOT_RELAY_LISTEN || "127.0.0.1:4141";
  const [host, portRaw] = String(raw || "").split(":");
  return {
    host: host || "127.0.0.1",
    port: Number(portRaw || "4141"),
  };
}

export function configuredModels(raw = process.env.PANGAEA_MODELS || process.env.COPILOT_RELAY_MODELS || process.env.PANGAEA_MODEL || "github-copilot-default") {
  const parsed = raw
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean)
    .map((item) => {
      const [id] = item.split("=", 1);
      return { id: id.trim(), object: "model", created: 0, owned_by: "github-copilot" };
    });
  const memberIDs = parsed
    .map((model) => model.id)
    .filter((id) => id && id !== "auto" && id !== "github-copilot-default" && id !== "copilot-default");
  return parsed.map((model) => {
    if (model.id === "github-copilot-default") {
      return { ...model, label: "copilot-default", kind: "alias" };
    }
    if (model.id === "copilot-default") {
      return { ...model, kind: "alias" };
    }
    if (model.id === "auto") {
      return { ...model, kind: "group", groupMembers: memberIDs };
    }
    return model;
  });
}

export function configuredModelStatus(raw) {
  const status = {};
  for (const model of configuredModels(raw)) {
    status[model.id] = {
      model: model.id,
      ...(model.label ? { label: model.label } : {}),
      ...(model.kind ? { kind: model.kind } : {}),
      ...(model.groupMembers?.length ? { groupMembers: model.groupMembers } : {}),
    };
  }
  return status;
}

function modelID(model) {
  return String(model?.id || "").trim();
}

export function openAIModelsFromSDKModels(models) {
  const out = [];
  const seen = new Set();
  for (const model of models || []) {
    const id = modelID(model);
    if (!id || seen.has(id)) continue;
    if (model?.policy?.state === "disabled") continue;
    seen.add(id);
    out.push({
      id,
      object: "model",
      created: 0,
      owned_by: "github-copilot",
    });
  }
  return out;
}

export function modelStatusFromSDKModels(models) {
  const out = {};
  const usableModelIDs = [];
  for (const model of models || []) {
    const id = modelID(model);
    if (!id || model?.policy?.state === "disabled") continue;
    usableModelIDs.push(id);
  }
  for (const model of models || []) {
    const id = modelID(model);
    if (!id || model?.policy?.state === "disabled") continue;
    const limits = model?.capabilities?.limits || {};
    const supports = model?.capabilities?.supports || {};
    const detail = { model: id };
    if (id === "auto") {
      detail.kind = "group";
      detail.groupMembers = usableModelIDs.filter((modelID) => modelID !== "auto");
    }
    if (typeof model?.name === "string" && model.name.trim() && model.name.trim() !== id) {
      detail.label = model.name.trim();
    }
    const maxTokens = Number(limits.max_context_window_tokens || limits.max_prompt_tokens || 0);
    if (Number.isFinite(maxTokens) && maxTokens > 0) {
      detail.maxTokens = maxTokens;
    }
    if (supports.vision === true) {
      detail.supportsImages = true;
    }
    if (Array.isArray(model?.supportedReasoningEfforts) && model.supportedReasoningEfforts.length > 0) {
      detail.supportedReasoningEfforts = model.supportedReasoningEfforts;
    }
    if (typeof model?.defaultReasoningEffort === "string" && model.defaultReasoningEffort.trim()) {
      detail.defaultReasoningEffort = model.defaultReasoningEffort.trim();
    }
    out[id] = detail;
  }
  return out;
}

const modelsTTLMS = Number(process.env.COPILOT_RELAY_MODELS_TTL_MS || "300000");
let modelsCache = { expiresAt: 0, value: null };
let modelsInflight = null;

async function discoverSDKModels() {
  prepareCopilotAuth();
  const client = newCopilotClient();
  try {
    await client.start();
    return await client.listModels();
  } finally {
    await client.stop().catch(() => {});
  }
}

async function getModelsCached() {
  const now = Date.now();
  if (modelsCache.value && modelsCache.expiresAt > now) {
    return modelsCache.value;
  }
  if (modelsInflight) {
    return modelsInflight;
  }
  modelsInflight = (async () => {
    try {
      const models = await discoverSDKModels();
      const usable = openAIModelsFromSDKModels(models);
      if (usable.length > 0) {
        modelsCache = { value: models, expiresAt: Date.now() + Math.max(1000, modelsTTLMS) };
        return models;
      }
    } finally {
      modelsInflight = null;
    }
    return [];
  })();
  return modelsInflight;
}

function defaultCopilotModel() {
  return (process.env.COPILOT_RELAY_MODEL || process.env.PANGAEA_COPILOT_MODEL || "auto").trim() || "auto";
}

function resolveCopilotModel(model) {
  const requested = String(model || "").trim();
  switch (requested) {
    case "":
    case "github-copilot-default":
    case "copilot-default":
      return defaultCopilotModel();
    default:
      return requested;
  }
}

function readJSON(req) {
  return new Promise((resolve, reject) => {
    let body = "";
    req.setEncoding("utf8");
    req.on("data", (chunk) => {
      body += chunk;
      if (body.length > 16 * 1024 * 1024) {
        reject(new Error("request body too large"));
        req.destroy();
      }
    });
    req.on("end", () => {
      try {
        resolve(body ? JSON.parse(body) : {});
      } catch (err) {
        reject(err);
      }
    });
    req.on("error", reject);
  });
}

function sendJSON(res, status, payload) {
  const body = JSON.stringify(payload);
  res.writeHead(status, {
    "content-type": "application/json",
    "content-length": Buffer.byteLength(body),
  });
  res.end(body);
}

function sendError(res, status, message) {
  sendJSON(res, status, { error: { message, type: "github_copilot_relay_error" } });
}

function messageText(message) {
  if (typeof message?.content === "string") {
    return message.content;
  }
  if (Array.isArray(message?.content)) {
    return message.content
      .map((part) => {
        if (typeof part === "string") return part;
        if (part?.type === "text") return part.text || "";
        return "";
      })
      .filter(Boolean)
      .join("\n");
  }
  return "";
}

function promptFromMessages(messages) {
  return (messages || [])
    .map((message) => {
      const text = messageText(message).trim();
      return text ? `[${message.role || "user"}]\n${text}` : "";
    })
    .filter(Boolean)
    .join("\n\n");
}

function chunkPayload(id, model, delta, finishReason = null) {
  return {
    id,
    object: "chat.completion.chunk",
    created: Math.floor(Date.now() / 1000),
    model,
    choices: [{ index: 0, delta, finish_reason: finishReason }],
  };
}

async function createSession(client, model, stream) {
  return client.createSession({ model, streaming: stream, onPermissionRequest: approveAllPermissions });
}

const authStatusTTLMS = Number(process.env.COPILOT_RELAY_AUTH_STATUS_TTL_MS || "60000");
let authStatusCache = { expiresAt: 0, value: null };
let authStatusInflight = null;

function publicAuthStatus(status) {
  const out = {
    isAuthenticated: Boolean(status?.isAuthenticated),
    authType: status?.authType || undefined,
    host: status?.host || undefined,
    login: status?.login || status?.user || status?.username || undefined,
    statusMessage: status?.statusMessage || undefined,
  };
  const subscription = publicSubscriptionStatus(status);
  if (subscription) {
    out.subscription = subscription;
  }
  return out;
}

function firstString(...values) {
  for (const value of values) {
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return undefined;
}

function publicSubscriptionStatus(status) {
  if (!status || typeof status !== "object") return undefined;
  const planStatus = status.planStatus && typeof status.planStatus === "object" ? status.planStatus : {};
  const planInfo = planStatus.planInfo && typeof planStatus.planInfo === "object" ? planStatus.planInfo : {};
  const userTier = status.userTier && typeof status.userTier === "object" ? status.userTier : {};
  const sourceSubscription = status.subscription && typeof status.subscription === "object" ? status.subscription : {};
  const tier = firstString(
    sourceSubscription.tier,
    sourceSubscription.plan_tier,
    status.sku,
    status.plan,
    status.planName,
    status.copilotPlan,
    status.subscriptionTier,
    status.subscriptionType,
    status.billingPlan,
    userTier.name,
    userTier.id,
    planInfo.planName,
  );
  const name = firstString(
    sourceSubscription.name,
    status.planName,
    userTier.name,
    planInfo.planName,
    status.plan,
    status.copilotPlan,
    status.sku,
  );
  const subscriptionStatus = firstString(
    sourceSubscription.status,
    status.subscriptionStatus,
    userTier.upgradeSubscriptionText,
  );
  const paidTier = firstString(sourceSubscription.paid_tier, sourceSubscription.paidTier, status.paidTier);
  const rateLimitTier = firstString(sourceSubscription.rate_limit_tier, sourceSubscription.rateLimitTier, status.rateLimitTier);
  if (!tier && !name && !subscriptionStatus && !paidTier && !rateLimitTier) {
    return undefined;
  }
  return {
    tier,
    name,
    status: subscriptionStatus,
    paid_tier: paidTier,
    rate_limit_tier: rateLimitTier,
    source: "github-copilot-sdk",
  };
}

async function getAuthStatusCached() {
  prepareCopilotAuth();
  const now = Date.now();
  if (authStatusCache.value && authStatusCache.expiresAt > now) {
    return authStatusCache.value;
  }
  if (authStatusInflight) {
    return authStatusInflight;
  }
  authStatusInflight = (async () => {
    const client = newCopilotClient();
    try {
      await client.start();
      const status = publicAuthStatus(await client.getAuthStatus());
      authStatusCache = { value: status, expiresAt: Date.now() + Math.max(1000, authStatusTTLMS) };
      return status;
    } finally {
      authStatusInflight = null;
      await client.stop().catch(() => {});
    }
  })();
  return authStatusInflight;
}

async function handleChat(req, res) {
  const body = await readJSON(req);
  const model = body.model || process.env.PANGAEA_MODEL || "github-copilot-default";
  const copilotModel = resolveCopilotModel(model);
  const prompt = promptFromMessages(body.messages);
  if (!prompt.trim()) {
    sendError(res, 400, "messages must contain text content");
    return;
  }

  const client = newCopilotClient();
  const id = `chatcmpl-${randomUUID()}`;
  try {
    prepareCopilotAuth();
    await client.start();
    if (body.stream) {
      res.writeHead(200, {
        "content-type": "text/event-stream",
        "cache-control": "no-cache",
        connection: "keep-alive",
      });
      const session = await createSession(client, copilotModel, true);
      session.on("assistant.message_delta", (event) => {
        const content = event?.data?.deltaContent || "";
        if (content) {
          res.write(`data: ${JSON.stringify(chunkPayload(id, model, { content }))}\n\n`);
        }
      });
      await session.sendAndWait({ prompt });
      res.write(`data: ${JSON.stringify(chunkPayload(id, model, {}, "stop"))}\n\n`);
      res.write("data: [DONE]\n\n");
      res.end();
      return;
    }

    const session = await createSession(client, copilotModel, false);
    const response = await session.sendAndWait({ prompt });
    const content = response?.data?.content || "";
    sendJSON(res, 200, {
      id,
      object: "chat.completion",
      created: Math.floor(Date.now() / 1000),
      model,
      choices: [{
        index: 0,
        message: { role: "assistant", content },
        finish_reason: "stop",
      }],
      usage: {
        prompt_tokens: 0,
        completion_tokens: 0,
        total_tokens: 0,
      },
    });
  } finally {
    await client.stop().catch(() => {});
  }
}

const { host, port } = parseListen(process.argv.slice(2));
const startedAt = new Date().toISOString();
const server = http.createServer(async (req, res) => {
  try {
    const url = new URL(req.url || "/", `http://${req.headers.host || "127.0.0.1"}`);
    if (req.method === "GET" && url.pathname === "/v1/health") {
      const payload = { status: "ok", service: "github-copilot", version: process.env.npm_package_version || "dev", started_at: startedAt };
      if (authStatusCache.value) {
        payload.auth = authStatusCache.value;
      }
      sendJSON(res, 200, payload);
      return;
    }
    if (req.method === "GET" && url.pathname === "/v1/auth/status") {
      try {
        sendJSON(res, 200, await getAuthStatusCached());
      } catch (err) {
        sendJSON(res, 200, {
          isAuthenticated: false,
          statusMessage: err instanceof Error ? err.message : String(err),
        });
      }
      return;
    }
    if (req.method === "GET" && url.pathname === "/v1/models") {
      const models = await getModelsCached().catch(() => []);
      const data = openAIModelsFromSDKModels(models);
      sendJSON(res, 200, { object: "list", data: data.length > 0 ? data : configuredModels() });
      return;
    }
    if (req.method === "GET" && url.pathname === "/v1/models/status") {
      const models = await getModelsCached().catch(() => []);
      const status = modelStatusFromSDKModels(models);
      sendJSON(res, 200, Object.keys(status).length > 0 ? status : configuredModelStatus());
      return;
    }
    if (req.method === "POST" && url.pathname === "/v1/chat/completions") {
      await handleChat(req, res);
      return;
    }
    sendError(res, 404, "not found");
  } catch (err) {
    sendError(res, 500, err instanceof Error ? err.message : String(err));
  }
});

if (import.meta.url === pathToFileURL(process.argv[1] || "").href) {
  server.listen(port, host, () => {
    console.error(`copilot-relay listening on ${host}:${port}`);
  });
}
