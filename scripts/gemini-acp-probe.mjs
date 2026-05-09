#!/usr/bin/env node
import { spawn } from "node:child_process";
import { createWriteStream, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { basename, resolve } from "node:path";
import { createInterface } from "node:readline";

const outDir = resolve(process.env.PANGAEA_GEMINI_FIXTURE_DIR || ".tmp/gemini-fixtures/acp");
const cwd = resolve(process.env.PANGAEA_GEMINI_ACP_CWD || process.cwd());
const model = process.env.PANGAEA_GEMINI_ACP_MODEL || "gemini-2.5-flash";
const prompt = process.env.PANGAEA_GEMINI_ACP_PROMPT || "Reply with exactly ACP_OK.";
const resourceFile = process.env.PANGAEA_GEMINI_ACP_RESOURCE_FILE || "";
const imageFile = process.env.PANGAEA_GEMINI_ACP_IMAGE_FILE || "";
const mcpCommand = process.env.PANGAEA_GEMINI_ACP_MCP_COMMAND || "";
const mcpArgs = process.env.PANGAEA_GEMINI_ACP_MCP_ARGS
  ? process.env.PANGAEA_GEMINI_ACP_MCP_ARGS.split(",").filter(Boolean)
  : [];
const sessionMode = process.env.PANGAEA_GEMINI_ACP_MODE || "";
mkdirSync(outDir, { recursive: true });

const rpcLog = createWriteStream(resolve(outDir, "rpc.ndjson"), { flags: "a" });
const stderrLog = createWriteStream(resolve(outDir, "stderr.log"), { flags: "a" });
const stdoutNoiseLog = createWriteStream(resolve(outDir, "stdout-noise.log"), { flags: "a" });

const childEnv = { ...process.env };
delete childEnv.NO_COLOR;
const child = spawn("gemini", ["--acp"], {
  cwd,
  env: {
    ...childEnv,
    TERM: process.env.TERM && process.env.TERM !== "dumb" ? process.env.TERM : "xterm-256color",
    COLORTERM: process.env.COLORTERM || "truecolor",
    FORCE_COLOR: process.env.FORCE_COLOR || "1",
  },
  stdio: ["pipe", "pipe", "pipe"],
});

const pending = new Map();
let nextID = 1;
let sessionID = "";
const summary = {
  agentMethods: [
    "initialize",
    "authenticate",
    "session/new",
    "session/load",
    "session/list",
    "session/fork",
    "session/resume",
    "session/close",
    "session/prompt",
    "session/set_mode",
    "session/set_model",
    "session/set_config_option",
    "session/cancel",
  ],
  clientMethodsObserved: [],
  initialize: null,
  newSession: null,
  setMode: null,
  prompt: null,
};
const clientMethods = new Set();

child.stderr.on("data", (chunk) => stderrLog.write(chunk));

const rl = createInterface({ input: child.stdout });
rl.on("line", (line) => {
  const trimmed = line.trim();
  if (!trimmed.startsWith("{")) {
    stdoutNoiseLog.write(line + "\n");
    return;
  }
  let message;
  try {
    message = JSON.parse(trimmed);
  } catch {
    stdoutNoiseLog.write(line + "\n");
    return;
  }
  rpcLog.write(JSON.stringify({ direction: "recv", message }) + "\n");
  if (message.method) {
    clientMethods.add(message.method);
    respondToClientRequest(message);
    return;
  }
  if (message.id !== undefined && pending.has(message.id)) {
    const { resolve: done, reject } = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) {
      reject(new Error(JSON.stringify(message.error)));
    } else {
      done(message.result);
    }
  }
});

function call(method, params = {}) {
  const id = nextID++;
  const message = { jsonrpc: "2.0", id, method, params };
  rpcLog.write(JSON.stringify({ direction: "send", message }) + "\n");
  child.stdin.write(JSON.stringify(message) + "\n");
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    setTimeout(() => {
      if (pending.delete(id)) {
        reject(new Error(`ACP request timed out: ${method}`));
      }
    }, Number(process.env.PANGAEA_GEMINI_ACP_TIMEOUT_MS || 120000));
  });
}

function notify(method, params = {}) {
  const message = { jsonrpc: "2.0", method, params };
  rpcLog.write(JSON.stringify({ direction: "send", message }) + "\n");
  child.stdin.write(JSON.stringify(message) + "\n");
}

function respond(id, result) {
  const message = { jsonrpc: "2.0", id, result };
  rpcLog.write(JSON.stringify({ direction: "send", message }) + "\n");
  child.stdin.write(JSON.stringify(message) + "\n");
}

function respondToClientRequest(message) {
  switch (message.method) {
    case "session/request_permission":
      respond(message.id, { outcome: { outcome: "selected", optionId: "allow_once" } });
      break;
    case "fs/read_text_file":
      respond(message.id, { content: "" });
      break;
    case "fs/write_text_file":
    case "terminal/create":
    case "terminal/output":
    case "terminal/wait_for_exit":
    case "terminal/kill":
    case "terminal/release":
      respond(message.id, {});
      break;
    default:
      if (message.id !== undefined) {
        respond(message.id, {});
      }
  }
}

function buildMcpServers() {
  if (!mcpCommand) return [];
  return [{
    name: "pangaea-fixture",
    command: mcpCommand,
    args: mcpArgs,
    env: [],
  }];
}

function buildPromptBlocks() {
  const blocks = [{ type: "text", text: prompt }];
  if (resourceFile) {
    const absolute = resolve(resourceFile);
    blocks.push({
      type: "resource",
      resource: {
        uri: `file://${absolute}`,
        mimeType: "text/markdown",
        text: readFileSync(absolute, "utf8"),
      },
      annotations: { audience: ["assistant"], priority: 0.8 },
    });
  }
  if (imageFile) {
    const absolute = resolve(imageFile);
    blocks.push({
      type: "image",
      data: readFileSync(absolute).toString("base64"),
      mimeType: guessMimeType(absolute),
      annotations: { audience: ["assistant"], priority: 0.5 },
    });
  }
  return blocks;
}

function guessMimeType(path) {
  const lower = basename(path).toLowerCase();
  if (lower.endsWith(".jpg") || lower.endsWith(".jpeg")) return "image/jpeg";
  if (lower.endsWith(".webp")) return "image/webp";
  return "image/png";
}

async function main() {
  try {
    summary.initialize = await call("initialize", {
      protocolVersion: 1,
      clientCapabilities: {
        auth: { terminal: false },
        fs: { readTextFile: false, writeTextFile: false },
        terminal: false,
      },
      clientInfo: { name: "pangaea-gemini-acp-probe", version: "0" },
    });
    summary.newSession = await call("session/new", { cwd, mcpServers: buildMcpServers() });
    sessionID = summary.newSession.sessionId;
    if (model && sessionID) {
      await call("session/set_model", { sessionId: sessionID, modelId: model });
    }
    if (sessionMode && sessionID) {
      summary.setMode = await call("session/set_mode", { sessionId: sessionID, modeId: sessionMode }).catch((err) => ({ error: String(err.message || err) }));
    }
    summary.prompt = await call("session/prompt", {
      sessionId: sessionID,
      prompt: buildPromptBlocks(),
    });
    if (sessionID && process.env.PANGAEA_GEMINI_ACP_CLOSE === "1") {
      await call("session/close", { sessionId: sessionID }).catch(() => {});
    }
    summary.clientMethodsObserved = [...clientMethods].sort();
    writeFileSync(resolve(outDir, "summary.json"), JSON.stringify(summary, null, 2));
  } finally {
    child.stdin.end();
    setTimeout(() => child.kill("SIGTERM"), 250);
    setTimeout(() => process.exit(process.exitCode ?? 0), 1000);
  }
}

main().catch((err) => {
  writeFileSync(resolve(outDir, "error.txt"), String(err.stack || err));
  child.kill("SIGTERM");
  process.exitCode = 1;
});
