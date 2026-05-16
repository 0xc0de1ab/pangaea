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

function activeCopilotAuth(config = readCopilotConfig(defaultCopilotConfigPath())) {
  if (!config || typeof config !== "object") return null;
  const user = config.lastLoggedInUser || (Array.isArray(config.loggedInUsers) ? config.loggedInUsers[0] : undefined) || {};
  const host = typeof user.host === "string" && user.host.trim() ? user.host.trim() : "https://github.com";
  const login = typeof user.login === "string" ? user.login.trim() : "";
  const tokens = config.copilotTokens && typeof config.copilotTokens === "object" ? config.copilotTokens : config.copilotToken;
  if (!tokens || typeof tokens !== "object") return null;
  const exactKey = login ? `${host}:${login}` : "";
  const token = (exactKey && typeof tokens[exactKey] === "string" ? tokens[exactKey] : undefined)
    || Object.values(tokens).find((value) => typeof value === "string" && value.trim());
  if (typeof token !== "string" || !token.trim()) return null;
  return { host, login, token: token.trim() };
}

function githubAPIBase(host) {
  const value = String(host || "").trim().replace(/\/+$/, "");
  if (!value || value === "https://github.com" || value === "http://github.com" || value === "github.com") {
    return "https://api.github.com";
  }
  if (value.startsWith("https://") || value.startsWith("http://")) {
    return `${value}/api/v3`;
  }
  return `https://${value}/api/v3`;
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
const quotaTTLMS = Number(process.env.COPILOT_RELAY_QUOTA_TTL_MS || "60000");
let quotaCache = { expiresAt: 0, value: null };
let quotaInflight = null;

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

export function quotaSnapshotsFromSDKQuota(result) {
  const snapshots = result?.quotaSnapshots && typeof result.quotaSnapshots === "object" ? result.quotaSnapshots : {};
  const out = {};
  for (const [key, value] of Object.entries(snapshots)) {
    if (!value || typeof value !== "object") continue;
    out[key] = {
      isUnlimitedEntitlement: Boolean(value.isUnlimitedEntitlement),
      entitlementRequests: Number(value.entitlementRequests || 0),
      usedRequests: Number(value.usedRequests || 0),
      usageAllowedWithExhaustedQuota: Boolean(value.usageAllowedWithExhaustedQuota),
      remainingPercentage: Number(value.remainingPercentage || 0),
      overage: Number(value.overage || 0),
      overageAllowedWithExhaustedQuota: Boolean(value.overageAllowedWithExhaustedQuota),
      hasQuota: Boolean(value.hasQuota),
      tokenBasedBilling: Boolean(value.tokenBasedBilling),
      ...(typeof value.resetDate === "string" && value.resetDate.trim() ? { resetDate: value.resetDate.trim() } : {}),
    };
  }
  return { quotaSnapshots: out };
}

function normalizedResetDate(value) {
  if (typeof value !== "string" || !value.trim()) return undefined;
  const raw = value.trim();
  if (/^\d{4}-\d{2}-\d{2}$/.test(raw)) {
    return `${raw}T00:00:00.000Z`;
  }
  const ts = new Date(raw);
  return Number.isNaN(ts.getTime()) ? undefined : ts.toISOString();
}

function quotaSnapshotFromRaw(snapshot, fallbackResetDate) {
  if (!snapshot || typeof snapshot !== "object") return undefined;
  const entitlement = Number(snapshot.entitlement ?? 0);
  const remaining = Number(snapshot.remaining ?? snapshot.quota_remaining ?? 0);
  const remainingPercentage = Number(snapshot.percent_remaining ?? (entitlement > 0 ? (remaining / entitlement) * 100 : 0));
  const isUnlimitedEntitlement = snapshot.unlimited === true || entitlement === -1;
  const entitlementRequests = isUnlimitedEntitlement ? 0 : entitlement;
  const usedRequests = isUnlimitedEntitlement ? 0 : Math.round(Math.max(0, entitlementRequests * (1 - remainingPercentage / 100)));
  return {
    isUnlimitedEntitlement,
    entitlementRequests,
    usedRequests,
    usageAllowedWithExhaustedQuota: Boolean(snapshot.overage_permitted),
    remainingPercentage,
    overage: Number(snapshot.overage_count || 0),
    overageAllowedWithExhaustedQuota: Boolean(snapshot.overage_permitted),
    hasQuota: Boolean(snapshot.has_quota),
    tokenBasedBilling: Boolean(snapshot.token_based_billing),
    ...(normalizedResetDate(fallbackResetDate) ? { resetDate: normalizedResetDate(fallbackResetDate) } : {}),
  };
}

function quotaSnapshotFromRemaining(limit, remaining, resetDate) {
  const entitlementRequests = Number(limit || 0);
  const remainingRequests = Number(remaining || 0);
  if (!Number.isFinite(entitlementRequests) || entitlementRequests <= 0) return undefined;
  const usedRequests = Math.max(0, entitlementRequests - remainingRequests);
  return {
    isUnlimitedEntitlement: false,
    entitlementRequests,
    usedRequests,
    usageAllowedWithExhaustedQuota: false,
    remainingPercentage: Math.max(0, Math.min(100, (remainingRequests / entitlementRequests) * 100)),
    overage: 0,
    overageAllowedWithExhaustedQuota: false,
    hasQuota: true,
    tokenBasedBilling: false,
    ...(normalizedResetDate(resetDate) ? { resetDate: normalizedResetDate(resetDate) } : {}),
  };
}

export function quotaSnapshotsFromCopilotUser(user) {
  const out = {};
  if (!user || typeof user !== "object") return { quotaSnapshots: out };
  const resetDate = user.quota_reset_date_utc || user.quota_reset_date;
  const snapshots = user.quota_snapshots && typeof user.quota_snapshots === "object" ? user.quota_snapshots : {};
  for (const [key, value] of Object.entries(snapshots)) {
    const mapped = quotaSnapshotFromRaw(value, resetDate);
    if (mapped) out[key] = mapped;
  }

  // GitHub Copilot Free users currently omit quota_snapshots and instead
  // expose monthly limits plus remaining counters. The official TUI uses this
  // path for the footer quota display.
  const monthly = user.monthly_quotas && typeof user.monthly_quotas === "object" ? user.monthly_quotas : {};
  const remaining = user.limited_user_quotas && typeof user.limited_user_quotas === "object" ? user.limited_user_quotas : {};
  const limitedReset = user.limited_user_reset_date || resetDate;
  for (const key of Object.keys(monthly)) {
    if (out[key]) continue;
    const mapped = quotaSnapshotFromRemaining(monthly[key], remaining[key], limitedReset);
    if (mapped) out[key] = mapped;
  }
  return { quotaSnapshots: out };
}

async function fetchCopilotUserQuota() {
  const auth = activeCopilotAuth();
  if (!auth) throw new Error("GitHub Copilot auth token unavailable");
  const response = await fetch(`${githubAPIBase(auth.host)}/copilot_internal/user`, {
    headers: {
      Authorization: `Bearer ${auth.token}`,
      Accept: "application/json",
      "User-Agent": "pangaea-copilot-quota-probe",
    },
  });
  if (!response.ok) {
    throw new Error(`GitHub Copilot user probe failed: HTTP ${response.status}`);
  }
  return quotaSnapshotsFromCopilotUser(await response.json());
}

async function getQuotaCached() {
  prepareCopilotAuth();
  const now = Date.now();
  if (quotaCache.value && quotaCache.expiresAt > now) {
    return quotaCache.value;
  }
  if (quotaInflight) {
    return quotaInflight;
  }
  quotaInflight = (async () => {
    const direct = await fetchCopilotUserQuota().catch(() => null);
    if (direct && Object.keys(direct.quotaSnapshots || {}).length > 0) {
      quotaCache = { value: direct, expiresAt: Date.now() + Math.max(1000, quotaTTLMS) };
      return direct;
    }
    const client = newCopilotClient();
    try {
      await client.start();
      if (!client.rpc?.account?.getQuota) {
        throw new Error("GitHub Copilot SDK does not expose account.getQuota");
      }
      const quota = quotaSnapshotsFromSDKQuota(await client.rpc.account.getQuota({}));
      quotaCache = { value: quota, expiresAt: Date.now() + Math.max(1000, quotaTTLMS) };
      return quota;
    } finally {
      quotaInflight = null;
      await client.stop().catch(() => {});
    }
  })();
  return quotaInflight;
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
    if (req.method === "GET" && url.pathname === "/v1/quota") {
      sendJSON(res, 200, await getQuotaCached());
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
