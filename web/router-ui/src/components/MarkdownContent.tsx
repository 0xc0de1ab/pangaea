import type { ReactNode } from "react";
import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import dockerfile from "highlight.js/lib/languages/dockerfile";
import go from "highlight.js/lib/languages/go";
import json from "highlight.js/lib/languages/json";
import javascript from "highlight.js/lib/languages/javascript";
import markdown from "highlight.js/lib/languages/markdown";
import python from "highlight.js/lib/languages/python";
import sql from "highlight.js/lib/languages/sql";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";

hljs.registerLanguage("bash", bash);
hljs.registerLanguage("dockerfile", dockerfile);
hljs.registerLanguage("go", go);
hljs.registerLanguage("json", json);
hljs.registerLanguage("javascript", javascript);
hljs.registerLanguage("markdown", markdown);
hljs.registerLanguage("python", python);
hljs.registerLanguage("sql", sql);
hljs.registerLanguage("typescript", typescript);
hljs.registerLanguage("xml", xml);
hljs.registerLanguage("yaml", yaml);

type Block =
  | { kind: "heading"; level: number; text: string }
  | { kind: "paragraph"; text: string }
  | { kind: "code"; lang: string; text: string }
  | { kind: "quote"; text: string }
  | { kind: "ul"; items: string[] }
  | { kind: "ol"; items: Array<{ value: number; text: string }> };

export function MarkdownContent({ content }: { content: string }) {
  const blocks = parseMarkdown(content);
  if (!blocks.length) {
    return null;
  }
  return (
    <div className="markdown-body">
      {blocks.map((block, index) => renderBlock(block, index))}
    </div>
  );
}

function parseMarkdown(content: string): Block[] {
  const lines = content.replace(/\r\n/g, "\n").split("\n");
  const blocks: Block[] = [];
  let index = 0;
  let lastOrdered = 0;
  let canContinueOrdered = false;

  while (index < lines.length) {
    const line = lines[index];
    if (!line.trim()) {
      index += 1;
      continue;
    }

    const fence = line.match(/^```(\S*)\s*$/);
    if (fence) {
      const code: string[] = [];
      index += 1;
      while (index < lines.length && !/^```\s*$/.test(lines[index])) {
        code.push(lines[index]);
        index += 1;
      }
      if (index < lines.length) {
        index += 1;
      }
      blocks.push({ kind: "code", lang: fence[1] || "", text: code.join("\n") });
      continue;
    }

    const heading = line.match(/^(#{1,6})\s+(.+)$/);
    if (heading) {
      blocks.push({ kind: "heading", level: heading[1].length, text: heading[2].trim() });
      lastOrdered = 0;
      canContinueOrdered = false;
      index += 1;
      continue;
    }

    const unordered = line.match(/^\s*[-*+]\s+(.+)$/);
    if (unordered) {
      const items: string[] = [];
      while (index < lines.length) {
        const item = lines[index].match(/^\s*[-*+]\s+(.+)$/);
        if (!item) break;
        items.push(item[1].trim());
        index += 1;
      }
      blocks.push({ kind: "ul", items });
      lastOrdered = 0;
      canContinueOrdered = false;
      continue;
    }

    const ordered = line.match(/^\s*(\d+)[.)]\s+(.+)$/);
    if (ordered) {
      const items: Array<{ value: number; text: string }> = [];
      let nextValue = Number(ordered[1]);
      if (canContinueOrdered && lastOrdered > 0 && nextValue <= lastOrdered) {
        nextValue = lastOrdered + 1;
      }
      while (index < lines.length) {
        const item = lines[index].match(/^\s*(\d+)[.)]\s+(.+)$/);
        if (!item) break;
        const explicit = Number(item[1]);
        const value = explicit > nextValue ? explicit : nextValue;
        items.push({ value, text: item[2].trim() });
        nextValue = value + 1;
        index += 1;
      }
      lastOrdered = items[items.length - 1]?.value ?? lastOrdered;
      canContinueOrdered = true;
      blocks.push({ kind: "ol", items });
      continue;
    }

    const quote = line.match(/^\s*>\s?(.*)$/);
    if (quote) {
      const quoted: string[] = [];
      while (index < lines.length) {
        const item = lines[index].match(/^\s*>\s?(.*)$/);
        if (!item) break;
        quoted.push(item[1]);
        index += 1;
      }
      blocks.push({ kind: "quote", text: quoted.join("\n") });
      lastOrdered = 0;
      canContinueOrdered = false;
      continue;
    }

    const paragraph: string[] = [];
    while (index < lines.length && lines[index].trim() && !isBlockStart(lines[index])) {
      paragraph.push(lines[index].trim());
      index += 1;
    }
    blocks.push({ kind: "paragraph", text: paragraph.join(" ") });
    lastOrdered = 0;
    canContinueOrdered = false;
  }
  return blocks;
}

function isBlockStart(line: string) {
  return /^```/.test(line) || /^(#{1,6})\s+/.test(line) || /^\s*[-*+]\s+/.test(line) || /^\s*\d+[.)]\s+/.test(line) || /^\s*>\s?/.test(line);
}

function renderBlock(block: Block, index: number) {
  switch (block.kind) {
    case "heading": {
      const Tag = `h${block.level}` as keyof JSX.IntrinsicElements;
      return <Tag key={index}>{renderInline(block.text)}</Tag>;
    }
    case "paragraph":
      return <p key={index}>{renderInline(block.text)}</p>;
    case "code": {
      const highlighted = highlightCode(block.lang, block.text);
      return (
        <pre key={index} className="markdown-code">
          {block.lang ? <span className="markdown-code-lang">{block.lang}</span> : null}
          <code dangerouslySetInnerHTML={{ __html: highlighted }} />
        </pre>
      );
    }
    case "quote":
      return <blockquote key={index}>{renderInline(block.text)}</blockquote>;
    case "ul":
      return <ul key={index}>{block.items.map((item, itemIndex) => <li key={itemIndex}>{renderInline(item)}</li>)}</ul>;
    case "ol":
      return (
        <ol key={index} start={block.items[0]?.value ?? 1}>
          {block.items.map((item, itemIndex) => <li key={itemIndex} value={item.value}>{renderInline(item.text)}</li>)}
        </ol>
      );
  }
}

function highlightCode(lang: string, text: string) {
  const language = normalizeLanguage(lang);
  if (language && hljs.getLanguage(language)) {
    return hljs.highlight(text, { language, ignoreIllegals: true }).value;
  }
  return escapeHTML(text);
}

function normalizeLanguage(lang: string) {
  switch (lang.trim().toLowerCase()) {
    case "sh":
    case "shell":
    case "zsh":
      return "bash";
    case "docker":
      return "dockerfile";
    case "js":
    case "jsx":
      return "javascript";
    case "ts":
    case "tsx":
      return "typescript";
    case "yml":
      return "yaml";
    case "html":
    case "svg":
      return "xml";
    default:
      return lang.trim().toLowerCase();
  }
}

function escapeHTML(text: string) {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#039;");
}

function renderInline(text: string): ReactNode[] {
  const nodes: ReactNode[] = [];
  const token = /(`[^`]+`|\*\*[^*]+\*\*|\[[^\]]+\]\((https?:\/\/[^)\s]+)\))/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = token.exec(text))) {
    if (match.index > lastIndex) {
      nodes.push(text.slice(lastIndex, match.index));
    }
    const raw = match[0];
    if (raw.startsWith("`")) {
      nodes.push(<code key={nodes.length}>{raw.slice(1, -1)}</code>);
    } else if (raw.startsWith("**")) {
      nodes.push(<strong key={nodes.length}>{raw.slice(2, -2)}</strong>);
    } else {
      const label = raw.match(/^\[([^\]]+)\]/)?.[1] ?? match[2];
      nodes.push(<a key={nodes.length} href={match[2]} target="_blank" rel="noreferrer">{label}</a>);
    }
    lastIndex = token.lastIndex;
  }
  if (lastIndex < text.length) {
    nodes.push(text.slice(lastIndex));
  }
  return nodes;
}
