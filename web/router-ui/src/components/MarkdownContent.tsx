import { useMemo, type ReactNode } from "react";
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
  | { kind: "table"; headers: string[]; aligns: TableAlign[]; rows: string[][] }
  | { kind: "ul"; items: string[] }
  | { kind: "ol"; items: Array<{ value: number; text: string }> };

type TableAlign = "left" | "center" | "right" | undefined;

export function MarkdownContent({ content, deferHighlight = false }: { content: string; deferHighlight?: boolean }) {
  const blocks = useMemo(() => parseMarkdown(content), [content]);
  if (!blocks.length) {
    return null;
  }
  return (
    <div className="markdown-body">
      {blocks.map((block, index) => renderBlock(block, index, deferHighlight))}
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
    const startIndex = index;
    const line = lines[index];
    if (!line.trim()) {
      index += 1;
      continue;
    }

    const fence = parseFenceStart(line);
    if (fence) {
      const code: string[] = [];
      index += 1;
      while (index < lines.length && !isFenceEnd(lines[index], fence)) {
        code.push(lines[index]);
        index += 1;
      }
      if (index < lines.length) {
        index += 1;
      }
      blocks.push({ kind: "code", lang: fence.lang, text: code.join("\n") });
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

    const table = parseTable(lines, index);
    if (table) {
      blocks.push(table.block);
      lastOrdered = 0;
      canContinueOrdered = false;
      index = table.nextIndex;
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
    if (index === startIndex) {
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
  return Boolean(parseFenceStart(line)) || /^(#{1,6})\s+/.test(line) || /^\s*[-*+]\s+/.test(line) || /^\s*\d+[.)]\s+/.test(line) || /^\s*>\s?/.test(line);
}

function parseTable(lines: string[], index: number): { block: Block; nextIndex: number } | null {
  if (index + 1 >= lines.length || !looksLikeTableRow(lines[index]) || !isTableDelimiterRow(lines[index + 1])) {
    return null;
  }
  const headers = splitTableRow(lines[index]);
  const delimiters = splitTableRow(lines[index + 1]);
  if (headers.length < 2 || delimiters.length < headers.length || !delimiters.slice(0, headers.length).every(isTableDelimiterCell)) {
    return null;
  }
  const aligns = delimiters.slice(0, headers.length).map(tableAlign);
  const rows: string[][] = [];
  let nextIndex = index + 2;
  while (nextIndex < lines.length && looksLikeTableRow(lines[nextIndex])) {
    const cells = splitTableRow(lines[nextIndex]);
    if (!cells.length) {
      break;
    }
    rows.push(normalizeTableCells(cells, headers.length));
    nextIndex += 1;
  }
  return { block: { kind: "table", headers, aligns, rows }, nextIndex };
}

function looksLikeTableRow(line: string) {
  return line.includes("|") && line.trim() !== "";
}

function isTableDelimiterRow(line: string) {
  const cells = splitTableRow(line);
  return cells.length >= 2 && cells.every(isTableDelimiterCell);
}

function isTableDelimiterCell(cell: string) {
  return /^:?-{3,}:?$/.test(cell.trim());
}

function tableAlign(cell: string): TableAlign {
  const value = cell.trim();
  const left = value.startsWith(":");
  const right = value.endsWith(":");
  if (left && right) return "center";
  if (right) return "right";
  if (left) return "left";
  return undefined;
}

function splitTableRow(line: string) {
  let value = line.trim();
  if (value.startsWith("|")) value = value.slice(1);
  if (value.endsWith("|") && !value.endsWith("\\|")) value = value.slice(0, -1);
  const cells: string[] = [];
  let cell = "";
  let inCode = false;
  for (let index = 0; index < value.length; index += 1) {
    const char = value[index];
    if (char === "\\") {
      const next = value[index + 1];
      if (next === "|" || next === "\\") {
        cell += next;
        index += 1;
      } else {
        cell += char;
      }
      continue;
    }
    if (char === "`") {
      inCode = !inCode;
      cell += char;
      continue;
    }
    if (char === "|" && !inCode) {
      cells.push(cell.trim());
      cell = "";
      continue;
    }
    cell += char;
  }
  cells.push(cell.trim());
  return cells;
}

function normalizeTableCells(cells: string[], length: number) {
  const out = cells.slice(0, length);
  while (out.length < length) {
    out.push("");
  }
  return out;
}

type FenceStart = {
  char: "`" | "~";
  length: number;
  lang: string;
};

function parseFenceStart(line: string): FenceStart | null {
  const match = line.match(/^\s*(`{3,}|~{3,})(.*)$/);
  if (!match) {
    return null;
  }
  const marker = match[1];
  const info = match[2].trim();
  const char = marker[0] as "`" | "~";
  if (char === "`" && info.includes("`")) {
    return null;
  }
  return {
    char,
    length: marker.length,
    lang: info.split(/\s+/)[0] || "",
  };
}

function isFenceEnd(line: string, start: FenceStart) {
  const match = line.match(/^\s*(`{3,}|~{3,})\s*$/);
  if (!match) {
    return false;
  }
  const marker = match[1];
  return marker[0] === start.char && marker.length >= start.length;
}

function renderBlock(block: Block, index: number, deferHighlight: boolean) {
  switch (block.kind) {
    case "heading": {
      const Tag = `h${block.level}` as keyof JSX.IntrinsicElements;
      return <Tag key={index}>{renderInline(block.text)}</Tag>;
    }
    case "paragraph":
      return <p key={index}>{renderInline(block.text)}</p>;
    case "code": {
      const highlighted = highlightCode(block.lang, block.text, deferHighlight);
      return (
        <pre key={index} className="markdown-code">
          {block.lang ? <span className="markdown-code-lang">{block.lang}</span> : null}
          <code dangerouslySetInnerHTML={{ __html: highlighted }} />
        </pre>
      );
    }
    case "quote":
      return <blockquote key={index}>{renderInline(block.text)}</blockquote>;
    case "table":
      return (
        <div className="markdown-table-frame" key={index}>
          <table className="markdown-table">
            <thead>
              <tr>
                {block.headers.map((header, cellIndex) => (
                  <th key={cellIndex} style={tableCellStyle(block.aligns[cellIndex])}>{renderInline(header)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {block.rows.map((row, rowIndex) => (
                <tr key={rowIndex}>
                  {row.map((cell, cellIndex) => (
                    <td key={cellIndex} style={tableCellStyle(block.aligns[cellIndex])}>{renderInline(cell)}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      );
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

function tableCellStyle(align: TableAlign) {
  return align ? { textAlign: align } : undefined;
}

function highlightCode(lang: string, text: string, deferHighlight: boolean) {
  if (deferHighlight) {
    return escapeHTML(text);
  }
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
