#!/usr/bin/env node

const protocolOrder = ["openai", "anthropic", "gemini"];
const protocolCapabilities = {
  openai: ["api.openai.chat", "api.openai.responses"],
  anthropic: ["api.anthropic.messages"],
  gemini: ["api.gemini.generateContent"],
};

const questions = [
  { prompt: "Reply with exactly OK.", expect: "OK" },
  { prompt: "What is 2 + 2? Reply with only 4.", expect: "4" },
  { prompt: "Reply with only the lowercase word pangaea.", expect: "pangaea" },
  { prompt: "Reply with only the two uppercase letters AI.", expect: "AI" },
  { prompt: "Reply with only the number 7.", expect: "7" },
];

const args = parseArgs(process.argv.slice(2));
const baseURL = stripTrailingSlash(args.base || "http://127.0.0.1:18080");
const token = args.token || "1";
const questionCount = Number.parseInt(args.questions || String(questions.length), 10);
const timeoutMs = Number.parseInt(args.timeoutMs || "180000", 10);
const providerFilter = splitCSV(args.provider || args.providers);
const protocolFilter = splitCSV(args.protocol || args.protocols);
const modeFilter = splitCSV(args.mode || args.modes);
const selectedQuestions = questions.slice(0, Math.max(1, Math.min(questions.length, questionCount)));

const failures = [];
const rows = [];

const providersPayload = await requestJSON("/router/v1/providers");
const providers = (providersPayload.providers || []).filter((provider) => {
  const id = provider.identity?.provider_instance_id || "";
  const service = provider.identity?.service || "";
  if (!providerFilter.length) {
    return true;
  }
  return providerFilter.includes(id) || providerFilter.includes(service);
});

if (!providers.length) {
  throw new Error("no providers matched");
}

for (const provider of providers) {
  const providerID = provider.identity?.provider_instance_id || "";
  const service = provider.identity?.service || "";
  const protocols = providerProtocols(provider).filter((protocol) => !protocolFilter.length || protocolFilter.includes(protocol));
  for (const protocol of protocols) {
    const model = preferredModelForProtocol(provider, protocol);
    if (!model) {
      recordFailure({ providerID, protocol, mode: "route", question: 0, error: "no model for protocol" });
      continue;
    }
    for (const mode of ["buffered", "stream"].filter((candidate) => !modeFilter.length || modeFilter.includes(candidate))) {
      const route = await dryRun(providerID, protocol, model, mode === "stream");
      if (!route.allowed) {
        recordFailure({ providerID, protocol, mode, question: 0, model, error: `route blocked: ${route.reason || "unknown"}` });
        continue;
      }
      const selected = route.selected_provider?.identity?.provider_instance_id || route.selected || "";
      if (selected !== providerID) {
        recordFailure({ providerID, protocol, mode, question: 0, model, error: `dry-run selected ${selected || "(none)"}` });
        continue;
      }
      for (let index = 0; index < selectedQuestions.length; index += 1) {
        const question = selectedQuestions[index];
        const started = Date.now();
        try {
          const content = await chat(protocol, mode, model, question.prompt, provider);
          const elapsed = Date.now() - started;
          const normalized = content.trim();
          if (!normalized) {
            throw new Error("empty response");
          }
          if (!normalized.toLowerCase().includes(question.expect.toLowerCase())) {
            throw new Error(`response missing expected marker ${JSON.stringify(question.expect)}: ${sample(normalized)}`);
          }
          rows.push({ providerID, service, protocol, mode, model, question: index + 1, elapsed, sample: sample(normalized) });
          console.log(formatPass({ providerID, protocol, mode, question: index + 1, elapsed, sample: sample(normalized) }));
        } catch (err) {
          recordFailure({ providerID, service, protocol, mode, question: index + 1, model, error: errorMessage(err) });
        }
      }
    }
  }
}

console.log("");
console.log(`checked ${rows.length + failures.length} calls: ${rows.length} passed, ${failures.length} failed`);
if (failures.length) {
  console.log("failures:");
  for (const failure of failures) {
    console.log(`- ${failure.providerID} ${failure.protocol}/${failure.mode} q${failure.question} model=${failure.model || ""}: ${failure.error}`);
  }
  process.exitCode = 1;
}

function parseArgs(argv) {
  const out = {};
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (!arg.startsWith("--")) {
      continue;
    }
    const eq = arg.indexOf("=");
    if (eq >= 0) {
      out[arg.slice(2, eq)] = arg.slice(eq + 1);
      continue;
    }
    const key = arg.slice(2);
    const next = argv[index + 1];
    if (next && !next.startsWith("--")) {
      out[key] = next;
      index += 1;
    } else {
      out[key] = "true";
    }
  }
  return out;
}

function splitCSV(value) {
  if (!value) {
    return [];
  }
  return String(value).split(",").map((part) => part.trim()).filter(Boolean);
}

function stripTrailingSlash(value) {
  return value.replace(/\/+$/, "");
}

async function dryRun(providerID, protocol, model, stream) {
  return requestJSON("/router/v1/routes/dry-run", {
    method: "POST",
    body: {
      model,
      api_dialect: protocol,
      stream,
    },
    okStatuses: [200, 409],
  }).catch((err) => {
    recordFailure({ providerID, protocol, mode: "dry-run", question: 0, model, error: errorMessage(err) });
    return { allowed: false, reason: errorMessage(err) };
  });
}

async function chat(protocol, mode, model, prompt, provider) {
  if (protocol === "openai") {
    return openAIChat(mode, model, prompt);
  }
  if (protocol === "anthropic") {
    return anthropicChat(mode, model, prompt, maxOutputTokens(provider, model));
  }
  return geminiChat(mode, model, prompt);
}

async function openAIChat(mode, model, prompt) {
  const body = {
    model,
    messages: [{ role: "user", content: prompt }],
    max_tokens: 64,
    stream: mode === "stream",
  };
  if (mode === "buffered") {
    const payload = await requestJSON("/router/v1/compat/v1/chat/completions", { method: "POST", body });
    return (payload.choices || []).map((choice) => choice.message?.content || "").join("");
  }
  return requestSSE("/router/v1/compat/v1/chat/completions", { method: "POST", body }, (payload) => {
    return payload.choices?.[0]?.delta?.content || "";
  });
}

async function anthropicChat(mode, model, prompt, upstreamMaxOutputTokens) {
  const body = {
    model,
    max_tokens: Math.min(upstreamMaxOutputTokens || 1024, 1024),
    messages: [{ role: "user", content: prompt }],
    stream: mode === "stream",
  };
  const headers = { "anthropic-version": "2023-06-01" };
  if (mode === "buffered") {
    const payload = await requestJSON("/router/v1/compat/v1/messages", { method: "POST", headers, body });
    return (payload.content || []).map((part) => part.text || "").join("");
  }
  return requestSSE("/router/v1/compat/v1/messages", { method: "POST", headers, body }, (payload) => {
    if (payload.type === "content_block_start") {
      return payload.content_block?.text || "";
    }
    return payload.type === "content_block_delta" ? payload.delta?.text || "" : "";
  });
}

async function geminiChat(mode, model, prompt) {
  const modelPath = encodeURIComponent(model);
  const body = {
    contents: [{ role: "user", parts: [{ text: prompt }] }],
    generationConfig: { maxOutputTokens: 64 },
  };
  if (mode === "buffered") {
    const payload = await requestJSON(`/router/v1/compat/v1beta/models/${modelPath}:generateContent`, { method: "POST", body });
    return extractGeminiText(payload);
  }
  return requestSSE(`/router/v1/compat/v1beta/models/${modelPath}:streamGenerateContent?alt=sse`, { method: "POST", body }, extractGeminiText);
}

function extractGeminiText(payload) {
  return (payload.candidates || [])
    .flatMap((candidate) => candidate.content?.parts || [])
    .map((part) => part.text || "")
    .join("");
}

async function requestJSON(path, options = {}) {
  const response = await fetchWithTimeout(path, options);
  const text = await response.text();
  if (!isOK(response.status, options.okStatuses)) {
    throw new Error(responseError(response, text));
  }
  if (!text.trim()) {
    return {};
  }
  return JSON.parse(text);
}

async function requestSSE(path, options = {}, extractDelta) {
  const response = await fetchWithTimeout(path, options);
  if (!isOK(response.status, options.okStatuses)) {
    throw new Error(responseError(response, await response.text()));
  }
  if (!response.body) {
    throw new Error("stream response body is empty");
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let content = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) {
      break;
    }
    buffer += decoder.decode(value, { stream: true });
    const consumed = consumeSSEBuffer(buffer, false, (payload) => {
      content += extractDelta(payload);
    });
    buffer = consumed;
  }
  buffer += decoder.decode();
  consumeSSEBuffer(buffer, true, (payload) => {
    content += extractDelta(payload);
  });
  return content;
}

async function fetchWithTimeout(path, options = {}) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(baseURL + path, {
      method: options.method || "GET",
      headers: requestHeaders(options.headers),
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: controller.signal,
    });
  } finally {
    clearTimeout(timer);
  }
}

function requestHeaders(extra = {}) {
  const headers = {
    Accept: "application/json",
    "Content-Type": "application/json",
    ...extra,
  };
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }
  return headers;
}

function isOK(status, okStatuses) {
  return (okStatuses || [200, 201, 202, 204]).includes(status);
}

function responseError(response, text) {
  try {
    const payload = JSON.parse(text);
    if (typeof payload.error === "string") {
      return payload.error;
    }
    if (payload.error?.message) {
      return payload.error.message;
    }
    return JSON.stringify(payload);
  } catch {
    return text || response.statusText || `HTTP ${response.status}`;
  }
}

function consumeSSEBuffer(buffer, flush, onPayload) {
  let cursor = 0;
  for (;;) {
    const boundary = findSSEFrameBoundary(buffer, cursor);
    if (!boundary) {
      break;
    }
    emitSSEFrame(buffer.slice(cursor, boundary.index), onPayload);
    cursor = boundary.nextIndex;
  }
  if (flush && cursor < buffer.length) {
    emitSSEFrame(buffer.slice(cursor), onPayload);
    return "";
  }
  return buffer.slice(cursor);
}

function findSSEFrameBoundary(buffer, start) {
  for (let index = start; index < buffer.length - 1; index += 1) {
    if (buffer[index] !== "\n") {
      continue;
    }
    if (buffer[index + 1] === "\n") {
      return { index, nextIndex: index + 2 };
    }
    if (buffer[index + 1] === "\r" && buffer[index + 2] === "\n") {
      return { index, nextIndex: index + 3 };
    }
  }
  return null;
}

function emitSSEFrame(frame, onPayload) {
  const data = frame
    .split(/\r?\n/)
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice(5).trimStart())
    .join("\n")
    .trim();
  if (!data || data === "[DONE]") {
    return;
  }
  const payload = JSON.parse(data);
  const error = streamPayloadError(payload);
  if (error) {
    throw new Error(error);
  }
  onPayload(payload);
}

function streamPayloadError(payload) {
  if (!payload || typeof payload !== "object") {
    return "";
  }
  const error = payload.error;
  if (!error) {
    return "";
  }
  if (typeof error === "string") {
    return error;
  }
  return error.message || error.error || error.detail || JSON.stringify(error);
}

function providerProtocols(provider) {
  const capabilities = providerCapabilitySet(provider);
  const matched = protocolOrder.filter((protocol) => protocolCapabilities[protocol].some((capability) => capabilities.has(capability)));
  return orderProtocolsForService(provider.identity?.service || "", matched);
}

function orderProtocolsForService(service, protocols) {
  const key = service.trim().toLowerCase().replace(/[_\s]+/g, "-");
  if (key === "minimax" || key.startsWith("minimax-")) {
    const preference = ["anthropic", "openai", "gemini"];
    return preference.filter((protocol) => protocols.includes(protocol));
  }
  return protocols;
}

function providerCapabilitySet(provider) {
  const capabilities = new Set(provider.capabilities || []);
  for (const model of provider.models || []) {
    for (const capability of model.capabilities || []) {
      capabilities.add(capability);
    }
  }
  return capabilities;
}

function providerModelsForProtocol(provider, protocol) {
  const models = provider.models || [];
  const matched = models.filter((model) => modelSupportsProtocol(model, protocol));
  return matched.length ? matched : models;
}

function modelSupportsProtocol(model, protocol) {
  const capabilities = new Set(model.capabilities || []);
  return protocolCapabilities[protocol].some((capability) => capabilities.has(capability));
}

function preferredModelForProtocol(provider, protocol) {
  const model = preferredModelsForService(provider.identity?.service || "", providerModelsForProtocol(provider, protocol))[0];
  return model?.id || model?.aliases?.[0] || "";
}

function preferredModelsForService(service, models) {
  const key = service.trim().toLowerCase().replace(/[_\s]+/g, "-");
  if (key !== "gemini" && !key.startsWith("gemini-")) {
    return models;
  }
  const hasConcrete = models.some((model) => model.kind !== "group");
  if (!hasConcrete) {
    return models;
  }
  return [...models].sort((a, b) => {
    const groupRank = Number(a.kind === "group") - Number(b.kind === "group");
    if (groupRank !== 0) {
      return groupRank;
    }
    const familyRank = geminiModelFamilyRank(a) - geminiModelFamilyRank(b);
    if (familyRank !== 0) {
      return familyRank;
    }
    const quotaRank = quotaRemaining(b) - quotaRemaining(a);
    if (quotaRank !== 0) {
      return quotaRank;
    }
    return models.indexOf(a) - models.indexOf(b);
  });
}

function quotaRemaining(model) {
  return model.quota?.remaining_pct ?? -1;
}

function geminiModelFamilyRank(model) {
  const id = String(model.id || "").toLowerCase();
  if (id.includes("flash-lite")) {
    return 0;
  }
  if (id.includes("flash")) {
    return 1;
  }
  if (id.includes("pro")) {
    return 3;
  }
  return 2;
}

function maxOutputTokens(provider, modelID) {
  const models = provider.models || [];
  const model = models.find((candidate) => candidate.id === modelID || (candidate.aliases || []).includes(modelID));
  return model?.max_output_tokens || model?.output_tokens || 0;
}

function recordFailure(failure) {
  failures.push(failure);
  console.error(`FAIL ${failure.providerID || ""} ${failure.protocol || ""}/${failure.mode || ""} q${failure.question || 0}: ${failure.error}`);
}

function formatPass(row) {
  return `PASS ${row.providerID} ${row.protocol}/${row.mode} q${row.question} ${row.elapsed}ms ${JSON.stringify(row.sample)}`;
}

function sample(text) {
  const oneLine = String(text).replace(/\s+/g, " ").trim();
  return oneLine.length > 80 ? oneLine.slice(0, 77) + "..." : oneLine;
}

function errorMessage(err) {
  if (err?.name === "AbortError") {
    return `timeout after ${timeoutMs}ms`;
  }
  return err?.message || String(err);
}
