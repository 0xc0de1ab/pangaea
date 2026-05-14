import type { ComponentProps } from "react";
import { Streamdown, defaultUrlTransform, type Components } from "streamdown";
import { createCodePlugin, type CodeHighlighterPlugin, type HighlightOptions } from "@streamdown/code";

const code = createMarkdownCodePlugin();
const streamdownPlugins = { code };

const codeLanguageAliases: Record<string, string> = {
  "c++": "cpp",
  "cc": "cpp",
  "cxx": "cpp",
  "h++": "cpp",
  "hh": "cpp",
  "hpp": "cpp",
  "hxx": "cpp",
};

export function createMarkdownCodePlugin(): CodeHighlighterPlugin {
  const base = createCodePlugin({ themes: ["dark-plus", "dark-plus"] });
  return {
    ...base,
    supportsLanguage(language) {
      return base.supportsLanguage(normalizeCodeLanguage(String(language)) as HighlightOptions["language"]);
    },
    highlight(options, callback) {
      return base.highlight({ ...options, language: normalizeCodeLanguage(String(options.language)) as HighlightOptions["language"] }, callback);
    },
  };
}

export function normalizeCodeLanguage(language: string) {
  const normalized = language.trim().toLowerCase().replace(/^language-/, "");
  return codeLanguageAliases[normalized] ?? normalized;
}

const markdownComponents: Components = {
  table: ({ children, className, node: _node, ...props }) => (
    <div className="markdown-table-frame">
      <table {...withoutClassAttribute(props)} className={classNames("markdown-table", className, classAttribute(props))}>
        {children}
      </table>
    </div>
  ),
};

export function MarkdownContent({ content, deferHighlight = false }: { content: string; deferHighlight?: boolean }) {
  if (!content.trim()) {
    return null;
  }
  return (
    <Streamdown
      animated={deferHighlight ? { animation: "fadeIn", duration: 120, sep: "word", stagger: 8 } : false}
      className="markdown-body"
      components={markdownComponents}
      controls={false}
      disallowedElements={["img"]}
      isAnimating={deferHighlight}
      lineNumbers={false}
      mode={deferHighlight ? "streaming" : "static"}
      plugins={streamdownPlugins}
      skipHtml
      urlTransform={(url, key, node) => {
        if (key === "src") {
          return null;
        }
        return defaultUrlTransform(url, key, node);
      }}
    >
      {content}
    </Streamdown>
  );
}

function classNames(...values: Array<ComponentProps<"div">["className"]>) {
  return values.filter(Boolean).join(" ") || undefined;
}

function classAttribute(props: Record<string, unknown>) {
  return typeof props.class === "string" ? props.class : undefined;
}

function withoutClassAttribute<T extends Record<string, unknown>>(props: T) {
  const { class: _className, ...rest } = props;
  return rest;
}
