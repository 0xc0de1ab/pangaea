import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, AtSign, Check, Copy, FileText, Image as ImageIcon, RefreshCw, Send, Trash2, X } from "lucide-react";
import { MarkdownContent } from "../components/MarkdownContent";
import { ServiceIcon } from "../components/ServiceIcon";
import { api, type DashboardChatContent, type DashboardChatContentPart, type DashboardChatMessage } from "../lib/api";
import { providerAccountLabel, providerInstanceID } from "../lib/derive";
import { copyText, cx, middleEllipsis } from "../lib/format";
import type { ServiceEndpoint } from "../lib/service-endpoints";
import type { ProviderModel, ProviderRegistration } from "../lib/types";

export type ChatWorkbenchTarget = {
  provider: ProviderRegistration;
  endpoint: ServiceEndpoint;
};

type ChatWorkbenchProps = {
  target: ChatWorkbenchTarget | null;
  token?: string;
  onClose: () => void;
};

type ChatMessage = Omit<DashboardChatMessage, "content"> & {
  id: string;
  content: string;
  pending?: boolean;
  error?: string;
  requestContent?: DashboardChatContent;
};

type ChatMode = "stream" | "buffered";
type ReasoningEffort = "" | "low" | "medium" | "high" | "xhigh";

type PendingAttachment = {
  id: string;
  name: string;
  mime: string;
  size: number;
  kind: "image" | "text";
  dataURL?: string;
  text?: string;
};

const panelExitMs = 260;
const transcriptBottomSlack = 96;
const maxImageAttachmentBytes = 10 * 1024 * 1024;
const maxTextAttachmentBytes = 256 * 1024;
const maxAttachments = 8;
const streamRenderFlushMs = 80;

export function ChatWorkbench({ target, token, onClose }: ChatWorkbenchProps) {
  const [renderTarget, setRenderTarget] = useState<ChatWorkbenchTarget | null>(target);
  const [exiting, setExiting] = useState(false);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState("");
  const [mode, setMode] = useState<ChatMode>("stream");
  const [selectedModel, setSelectedModel] = useState("");
  const [reasoningEffort, setReasoningEffort] = useState<ReasoningEffort>("");
  const [attachments, setAttachments] = useState<PendingAttachment[]>([]);
  const [attachmentError, setAttachmentError] = useState("");
  const [dragActive, setDragActive] = useState(false);
  const [busy, setBusy] = useState(false);
  const [copiedMessageID, setCopiedMessageID] = useState<string | null>(null);
  const [stickToBottom, setStickToBottom] = useState(true);
  const transcriptRef = useRef<HTMLDivElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const targetKey = target ? `${providerInstanceID(target.provider)}:${target.endpoint.id}` : "";
  const transcript = useMemo(() => messages.filter((message) => !message.pending && !message.error).map(({ role, content, requestContent }) => ({ role, content: requestContent ?? content })), [messages]);
  const activeTarget = target ?? renderTarget;
  const modelOptions = useMemo(() => activeTarget ? chatModelOptions(activeTarget.endpoint) : [], [activeTarget]);
  const thinkingLevels = useMemo(() => thinkingLevelsForModel(selectedModel, activeTarget?.provider), [activeTarget?.provider, selectedModel]);

  useEffect(() => {
    if (target) {
      setRenderTarget(target);
      setExiting(false);
      return;
    }
    if (!renderTarget) return;
    setExiting(true);
    const timeout = window.setTimeout(() => {
      setRenderTarget(null);
      setExiting(false);
    }, panelExitMs);
    return () => window.clearTimeout(timeout);
  }, [renderTarget, target]);

  useEffect(() => {
    if (!target) return;
    setMessages([]);
    setInput("");
    setMode(target.endpoint.supportsStream ? "stream" : "buffered");
    const options = chatModelOptions(target.endpoint);
    const initialModel = options[0]?.value || target.endpoint.model;
    setSelectedModel(initialModel);
    setReasoningEffort(defaultReasoningEffort(initialModel, target.provider));
    setAttachments([]);
    setAttachmentError("");
    setDragActive(false);
    setBusy(false);
    setCopiedMessageID(null);
    setStickToBottom(true);
  }, [targetKey, target]);

  useEffect(() => {
    if (!stickToBottom) return;
    const frame = window.requestAnimationFrame(() => scrollTranscriptToBottom("auto"));
    const settle = window.setTimeout(() => scrollTranscriptToBottom("auto"), 80);
    return () => {
      window.cancelAnimationFrame(frame);
      window.clearTimeout(settle);
    };
  }, [messages, busy, stickToBottom]);

  if (!activeTarget) {
    return null;
  }

  const { provider, endpoint } = activeTarget;
  const activeMode = endpoint.supportsStream ? mode : "buffered";
  const activeModel = selectedModel || endpoint.model;
  const activeModelInfo = endpoint.models?.find((model) => model.id === activeModel || (model.aliases ?? []).includes(activeModel));
  const activeMaxOutputTokens = activeModelInfo?.max_output_tokens;
  const activeReasoningEffort = thinkingLevels.includes(reasoningEffort) ? reasoningEffort : "";
  const activePath = endpoint.protocol === "gemini" ? `/v1beta/models/${activeModel}:${activeMode === "stream" ? "streamGenerateContent" : "generateContent"}` : activeMode === "stream" ? endpoint.streamPath : endpoint.chatPath;

  async function sendMessage() {
    const prompt = input.trim();
    if ((!prompt && attachments.length === 0) || busy) {
      return;
    }
    const requestContent = chatContentFromInput(prompt, attachments);
    const userMessage: ChatMessage = { id: crypto.randomUUID(), role: "user", content: displayMessageContent(prompt, attachments), requestContent };
    const assistantID = crypto.randomUUID();
    const assistantMessage: ChatMessage = { id: assistantID, role: "assistant", content: "", pending: true };
    const outbound = [...transcript, { role: "user" as const, content: requestContent }];
    setStickToBottom(true);
    setMessages((current) => [...current, userMessage, assistantMessage]);
    setInput("");
    setAttachments([]);
    setAttachmentError("");
    setBusy(true);
    let pendingStreamDelta = "";
    let streamFlushTimer: number | null = null;
    const flushStreamDelta = () => {
      if (!pendingStreamDelta) {
        streamFlushTimer = null;
        return;
      }
      const delta = pendingStreamDelta;
      pendingStreamDelta = "";
      streamFlushTimer = null;
      setMessages((current) => current.map((message) => message.id === assistantID ? { ...message, content: message.content + delta } : message));
    };
    const enqueueStreamDelta = (delta: string) => {
      if (!delta) return;
      pendingStreamDelta += delta;
      if (streamFlushTimer !== null) return;
      streamFlushTimer = window.setTimeout(flushStreamDelta, streamRenderFlushMs);
    };
    try {
      if (activeMode === "stream") {
        const result = await api.streamingChat(endpoint.protocol, activeModel, outbound, token, enqueueStreamDelta, activeReasoningEffort || undefined, activeMaxOutputTokens);
        if (streamFlushTimer !== null) {
          window.clearTimeout(streamFlushTimer);
          streamFlushTimer = null;
        }
        flushStreamDelta();
        setMessages((current) => current.map((message) => message.id === assistantID ? { ...message, content: result.content || message.content, pending: false } : message));
      } else {
        const result = await api.bufferedChat(endpoint.protocol, activeModel, outbound, token, activeReasoningEffort || undefined, activeMaxOutputTokens);
        setMessages((current) => current.map((message) => message.id === assistantID ? { ...message, content: result.content || "(empty response)", pending: false } : message));
      }
    } catch (err) {
      if (streamFlushTimer !== null) {
        window.clearTimeout(streamFlushTimer);
        streamFlushTimer = null;
      }
      const error = err instanceof Error ? err.message : "Chat request failed";
      setMessages((current) => current.map((message) => message.id === assistantID ? { ...message, content: "", pending: false, error } : message));
    } finally {
      setBusy(false);
    }
  }

  async function addFiles(fileList: FileList | File[]) {
    const files = Array.from(fileList);
    if (!files.length) return;
    setAttachmentError("");
    const next: PendingAttachment[] = [];
    for (const file of files) {
      if (attachments.length + next.length >= maxAttachments) {
        setAttachmentError(`Up to ${maxAttachments} attachments per message`);
        break;
      }
      try {
        next.push(await attachmentFromFile(file));
      } catch (err) {
        setAttachmentError(err instanceof Error ? err.message : "Attachment failed");
      }
    }
    if (next.length) {
      setAttachments((current) => [...current, ...next].slice(0, maxAttachments));
    }
  }

  function removeAttachment(id: string) {
    setAttachments((current) => current.filter((attachment) => attachment.id !== id));
  }

  function copyMessage(message: ChatMessage) {
    if (!message.content) return;
    copyText(message.content);
    setCopiedMessageID(message.id);
    window.setTimeout(() => {
      setCopiedMessageID((current) => current === message.id ? null : current);
    }, 1200);
  }

  function updateStickToBottom() {
    const element = transcriptRef.current;
    if (!element) return;
    setStickToBottom(isNearTranscriptBottom(element));
  }

  function jumpToLatest() {
    setStickToBottom(true);
    scrollTranscriptToBottom("smooth");
  }

  function scrollTranscriptToBottom(behavior: ScrollBehavior) {
    const element = transcriptRef.current;
    if (!element) return;
    element.scrollTo({ top: element.scrollHeight, behavior });
  }

  return (
    <div className={cx("chat-layer", exiting && "is-exiting")} role="presentation">
      <button className="chat-scrim" type="button" aria-label="Close chat" onClick={onClose} />
      <aside
        className={cx("chat-workbench", dragActive && "is-dragging-file")}
        aria-label={`${endpoint.label} chat`}
        onDragEnter={(event) => {
          event.preventDefault();
          if (!busy) setDragActive(true);
        }}
        onDragOver={(event) => {
          event.preventDefault();
          if (!busy) event.dataTransfer.dropEffect = "copy";
        }}
        onDragLeave={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
            setDragActive(false);
          }
        }}
        onDrop={(event) => {
          event.preventDefault();
          setDragActive(false);
          if (!busy) void addFiles(event.dataTransfer.files);
        }}
      >
        <div className="chat-header">
          <div className="chat-title-row">
            <ServiceIcon service={endpoint.protocol} size={30} label={endpoint.protocolLabel} />
            <div>
              <h2>{endpoint.label} Chat</h2>
              <p>
                <span className="mono">{middleEllipsis(providerInstanceID(provider), 18, 12)}</span>
                <span>{providerAccountLabel(provider)}</span>
              </p>
            </div>
          </div>
          <button className="icon-button" type="button" aria-label="Close chat" onClick={onClose}>
            <X aria-hidden="true" size={18} />
          </button>
        </div>

        <div className="chat-toolbar">
          <div className="segmented-control" role="group" aria-label="Response mode">
            <button className={cx(activeMode === "stream" && "active")} type="button" disabled={!endpoint.supportsStream || busy} onClick={() => setMode("stream")}>
              Stream
            </button>
            <button className={cx(activeMode === "buffered" && "active")} type="button" disabled={busy} onClick={() => setMode("buffered")}>
              Buffered
            </button>
          </div>
          <div className="chat-route">
            <label className="chat-select-label">
              <span>Model</span>
              <select value={activeModel} disabled={busy} onChange={(event) => setSelectedModel(event.target.value)}>
                {modelOptions.map((option) => (
                  <option value={option.value} key={option.value}>{option.label}</option>
                ))}
              </select>
            </label>
            {thinkingLevels.length ? (
              <label className="chat-select-label compact">
                <span>Thinking</span>
                <select value={reasoningEffort} disabled={busy} onChange={(event) => setReasoningEffort(event.target.value as ReasoningEffort)}>
                  <option value="">Default</option>
                  {thinkingLevels.map((level) => (
                    <option value={level} key={level}>{thinkingLabel(level)}</option>
                  ))}
                </select>
              </label>
            ) : null}
            <span className="mono">{activePath}</span>
          </div>
          <button className="icon-button small" type="button" title="Clear chat" disabled={busy || messages.length === 0} onClick={() => setMessages([])}>
            <Trash2 aria-hidden="true" size={15} />
          </button>
        </div>

        <div className={cx("chat-progress", busy && "is-active")} aria-hidden={!busy}>
          <span />
        </div>

        <div className="chat-transcript" ref={transcriptRef} onScroll={updateStickToBottom}>
          {messages.length === 0 ? (
            <div className="chat-empty">No messages</div>
          ) : messages.map((message) => (
            <article className={cx("chat-message", message.role === "user" ? "user" : "assistant")} key={message.id}>
              <div className="chat-message-meta">
                <div className="chat-message-role">{message.role === "user" ? "You" : endpoint.label}</div>
                <button className={cx("chat-copy-button", copiedMessageID === message.id && "copied")} type="button" title="Copy Markdown" aria-label="Copy message as Markdown" disabled={!message.content} onClick={() => copyMessage(message)}>
                  {copiedMessageID === message.id ? <Check aria-hidden="true" size={13} /> : <Copy aria-hidden="true" size={13} />}
                </button>
              </div>
              <div className="chat-bubble">
                {message.pending && !message.content ? <TypingSpinner /> : null}
                {message.error ? (
                  <div className="inline-error endpoint-error">{message.error}</div>
                ) : message.content ? (
                  message.pending ? <div className="streaming-plain-text">{message.content}</div> : <MarkdownContent content={message.content} />
                ) : null}
              </div>
            </article>
          ))}
          {!stickToBottom && messages.length > 0 ? (
            <button className="jump-to-latest" type="button" onClick={jumpToLatest}>
              <ArrowDown aria-hidden="true" size={14} />
              Latest
            </button>
          ) : null}
        </div>

        <form
          className="chat-composer"
          onSubmit={(event) => {
            event.preventDefault();
            void sendMessage();
          }}
        >
          <textarea
            value={input}
            onChange={(event) => setInput(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                void sendMessage();
              }
            }}
            disabled={busy}
            placeholder="Message"
            rows={3}
          />
          <input
            ref={fileInputRef}
            type="file"
            multiple
            accept="image/png,image/jpeg,image/webp,image/gif,text/*,.txt,.md,.json,.yaml,.yml,.csv,.log,.go,.ts,.tsx,.js,.jsx,.py,.rs,.java,.kt,.sh,.sql,.xml,.html,.css"
            hidden
            onChange={(event) => {
              if (event.currentTarget.files) void addFiles(event.currentTarget.files);
              event.currentTarget.value = "";
            }}
          />
          <button className="icon-button attach-button" type="button" title="Attach files" aria-label="Attach files" disabled={busy} onClick={() => fileInputRef.current?.click()}>
            <AtSign aria-hidden="true" size={16} />
          </button>
          <button className="button primary" type="submit" disabled={(!input.trim() && attachments.length === 0) || busy}>
            {busy ? <RefreshCw aria-hidden="true" className="spin" size={15} /> : <Send aria-hidden="true" size={15} />}
            Send
          </button>
        </form>
        {attachments.length || attachmentError ? (
          <div className="attachment-tray" aria-live="polite">
            {attachments.map((attachment) => (
              <span className={cx("attachment-chip", attachment.kind)} key={attachment.id} title={`${attachment.name} (${formatBytes(attachment.size)})`}>
                {attachment.kind === "image" ? <ImageIcon aria-hidden="true" size={13} /> : <FileText aria-hidden="true" size={13} />}
                <span>{attachment.name}</span>
                <button type="button" aria-label={`Remove ${attachment.name}`} onClick={() => removeAttachment(attachment.id)}>
                  <X aria-hidden="true" size={12} />
                </button>
              </span>
            ))}
            {attachmentError ? <span className="attachment-error">{attachmentError}</span> : null}
          </div>
        ) : null}
      </aside>
    </div>
  );
}

function isNearTranscriptBottom(element: HTMLElement) {
  return element.scrollHeight - element.scrollTop - element.clientHeight <= transcriptBottomSlack;
}

function chatModelOptions(endpoint: ServiceEndpoint) {
  const options = new Map<string, { value: string; label: string }>();
  for (const model of endpoint.models ?? []) {
    options.set(model.id, { value: model.id, label: modelLabel(model) });
  }
  if (endpoint.model && !options.has(endpoint.model)) {
    options.set(endpoint.model, { value: endpoint.model, label: `${endpoint.model} (route alias)` });
  }
  if (options.size === 0 && endpoint.model) {
    options.set(endpoint.model, { value: endpoint.model, label: endpoint.model });
  }
  return [...options.values()];
}

function modelLabel(model: ProviderModel) {
  const providerAlias = (model.aliases ?? []).find((alias) => /\s/.test(alias));
  return providerAlias && providerAlias !== model.id ? `${providerAlias} (${model.id})` : model.id;
}

function thinkingLevelsForModel(model: string, provider?: ProviderRegistration): ReasoningEffort[] {
  const service = provider?.identity.service?.toLowerCase() ?? "";
  if (!service.includes("codex") && !/^gpt-5/.test(model)) {
    return [];
  }
  return ["low", "medium", "high", "xhigh"];
}

function defaultReasoningEffort(model: string, provider?: ProviderRegistration): ReasoningEffort {
  return thinkingLevelsForModel(model, provider).includes("medium") ? "medium" : "";
}

function thinkingLabel(level: ReasoningEffort) {
  switch (level) {
    case "low":
      return "Low";
    case "medium":
      return "Medium";
    case "high":
      return "High";
    case "xhigh":
      return "Extra High";
    default:
      return "Default";
  }
}

function chatContentFromInput(prompt: string, attachments: PendingAttachment[]): DashboardChatContent {
  const textParts = [prompt];
  for (const attachment of attachments) {
    if (attachment.kind === "text" && attachment.text !== undefined) {
      textParts.push(`Attached file: ${attachment.name}\n\n\`\`\`\n${attachment.text}\n\`\`\``);
    }
  }
  const parts: DashboardChatContentPart[] = [];
  const text = textParts.filter((part) => part.trim()).join("\n\n");
  if (text) {
    parts.push({ type: "text", text });
  }
  for (const attachment of attachments) {
    if (attachment.kind === "image" && attachment.dataURL) {
      parts.push({ type: "image_url", image_url: { url: attachment.dataURL } });
    }
  }
  return parts.length === 1 && parts[0].type === "text" ? parts[0].text : parts;
}

function displayMessageContent(prompt: string, attachments: PendingAttachment[]) {
  const lines = [prompt.trim()].filter(Boolean);
  for (const attachment of attachments) {
    lines.push(attachment.kind === "image" ? `[Image: ${attachment.name}]` : `[File: ${attachment.name}]`);
  }
  return lines.join("\n\n") || "[Attachment]";
}

async function attachmentFromFile(file: File): Promise<PendingAttachment> {
  const mime = file.type || mimeFromName(file.name);
  if (mime.startsWith("image/")) {
    if (!supportedImageMIME(mime)) {
      throw new Error(`${file.name}: unsupported image type`);
    }
    if (file.size > maxImageAttachmentBytes) {
      throw new Error(`${file.name}: image is larger than ${formatBytes(maxImageAttachmentBytes)}`);
    }
    return {
      id: crypto.randomUUID(),
      name: file.name,
      mime,
      size: file.size,
      kind: "image",
      dataURL: await readAsDataURL(file),
    };
  }
  if (!isTextLikeFile(file, mime)) {
    throw new Error(`${file.name}: only images and text files are supported`);
  }
  if (file.size > maxTextAttachmentBytes) {
    throw new Error(`${file.name}: text file is larger than ${formatBytes(maxTextAttachmentBytes)}`);
  }
  return {
    id: crypto.randomUUID(),
    name: file.name,
    mime,
    size: file.size,
    kind: "text",
    text: await file.text(),
  };
}

function readAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error(`${file.name}: read failed`));
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.readAsDataURL(file);
  });
}

function supportedImageMIME(mime: string) {
  return ["image/png", "image/jpeg", "image/webp", "image/gif"].includes(mime.toLowerCase());
}

function isTextLikeFile(file: File, mime: string) {
  if (mime.startsWith("text/") || ["application/json", "application/xml", "application/yaml", "application/x-yaml"].includes(mime)) {
    return true;
  }
  return /\.(txt|md|json|ya?ml|csv|log|go|tsx?|jsx?|py|rs|java|kt|sh|sql|xml|html|css)$/i.test(file.name);
}

function mimeFromName(name: string) {
  if (/\.png$/i.test(name)) return "image/png";
  if (/\.jpe?g$/i.test(name)) return "image/jpeg";
  if (/\.webp$/i.test(name)) return "image/webp";
  if (/\.gif$/i.test(name)) return "image/gif";
  if (/\.json$/i.test(name)) return "application/json";
  if (/\.ya?ml$/i.test(name)) return "application/yaml";
  return "text/plain";
}

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function TypingSpinner() {
  return (
    <span className="typing-spinner" aria-label="Generating">
      <i />
      <i />
      <i />
    </span>
  );
}
