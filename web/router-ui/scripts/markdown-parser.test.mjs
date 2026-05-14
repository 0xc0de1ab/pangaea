import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MarkdownContent, createMarkdownCodePlugin, normalizeCodeLanguage } from "../node_modules/.tmp/markdown-test/components/MarkdownContent.js";

const streamdownCode = createMarkdownCodePlugin();

const nestedCapabilities = `
*   **가능한 것**:
    *   메소드 바디(내용) 수정
    *   기존 메소드의 로직 변경
    *   상수 풀(Constant Pool) 수정
*   **불가능한 것 (Schema 변경 불가)**:
    *   새로운 필드(변수) 추가
    *   새로운 메소드 추가
`;

const nestedHtml = renderMarkdown(nestedCapabilities);
assert.match(nestedHtml, /가능한 것/);
assertNestedBeforeParentClose(nestedHtml, "가능한 것", "메소드 바디");
assertNestedBeforeParentClose(nestedHtml, "불가능한 것", "새로운 필드");

const tableHtml = renderMarkdown(`
| 개념 | Go에서의 방법 |
|---|---|
| 함수 값 | \`func\`를 변수에 저장 |
| 고차 함수 | 함수를 인자로 받거나 반환 |
`);
assert.match(tableHtml, /<table/);
assert.match(tableHtml, /<th[^>]*>개념<\/th>/);
assert.match(tableHtml, /<td[^>]*>함수 값<\/td>/);

const codeHtml = renderMarkdown(`
\`\`\`go
package main

import "fmt"

func main() {
	fmt.Println("hi")
}
\`\`\`
`);
assert.match(codeHtml, /data-streamdown="code-block"/);
assert.match(codeHtml, /data-streamdown="code-block-body"/);
assert.match(codeHtml, /data-language="go"/);

assert.equal(normalizeCodeLanguage("cpp"), "cpp");
assert.equal(normalizeCodeLanguage("c++"), "cpp");
assert.equal(normalizeCodeLanguage("cc"), "cpp");
assert.equal(normalizeCodeLanguage("cxx"), "cpp");
assert.equal(normalizeCodeLanguage("hpp"), "cpp");
assert(streamdownCode.supportsLanguage("cpp"), "streamdown/code should support cpp fences");
assert(streamdownCode.supportsLanguage("c++"), "streamdown/code should support c++ fences through aliases");
assert(streamdownCode.supportsLanguage("cc"), "streamdown/code should support cc fences through aliases");
assert(streamdownCode.supportsLanguage("cxx"), "streamdown/code should support cxx fences through aliases");

const goShikiResult = await highlightWithStreamdownCode(`package main

import "fmt"

func main() {
\tfmt.Println("hi")
}
`, "go");
assert(goShikiResult.tokens.flat().some((token) => token.content === "package" && tokenColor(token)), "streamdown/code did not highlight Go keyword");
assert(goShikiResult.tokens.flat().some((token) => token.content === "fmt" && tokenColor(token)), "streamdown/code did not highlight Go import path");
assert.equal(tokenColor(goShikiResult.tokens.flat().find((token) => token.content === "package")), "#569CD6");

const cppShikiResult = await highlightWithStreamdownCode(`#include <iostream>

int main() {
\tstd::cout << "hi" << std::endl;
\treturn 0;
}
`, "cxx");
assert(cppShikiResult.tokens.flat().some((token) => token.content === "#include" && tokenColor(token)), "streamdown/code did not highlight C++ include directive");
assert(cppShikiResult.tokens.flat().some((token) => token.content === "main" && tokenColor(token)), "streamdown/code did not highlight C++ function name");
assert(cppShikiResult.tokens.flat().some((token) => token.content === '"hi"' && tokenColor(token)), "streamdown/code did not highlight C++ string literal");

const appCSS = readFileSync(new URL("../src/styles/app.css", import.meta.url), "utf8");
assert.match(appCSS, /\[data-streamdown="code-block-body"\]\s+code\s+>\s+span\s*\{[^}]*display:\s*block/s, "Streamdown code lines must render as block rows without Tailwind");
assert.match(appCSS, /\[data-streamdown="code-block-body"\]\s+span\s*\{[^}]*--sdm-c/s, "Streamdown syntax token colors must be applied without Tailwind");

console.log("markdown renderer fixtures passed");

function renderMarkdown(content) {
  return renderToStaticMarkup(React.createElement(MarkdownContent, { content }));
}

function tokenColor(token) {
  return token.color ?? token.htmlStyle?.color ?? token.htmlStyle?.["--shiki-dark"] ?? "";
}

async function highlightWithStreamdownCode(source, language) {
  const immediate = streamdownCode.highlight({
    code: source,
    language,
    themes: streamdownCode.getThemes(),
  });
  if (immediate) {
    return immediate;
  }
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("streamdown/code highlight timed out")), 5_000);
    streamdownCode.highlight(
      {
        code: source,
        language,
        themes: streamdownCode.getThemes(),
      },
      (result) => {
        clearTimeout(timeout);
        resolve(result);
      },
    );
  });
}

function assertNestedBeforeParentClose(html, parentText, childText) {
  const parentIndex = html.indexOf(parentText);
  assert.notEqual(parentIndex, -1, `parent text not found: ${parentText}`);
  const parentListItemStart = html.lastIndexOf("<li", parentIndex);
  assert.notEqual(parentListItemStart, -1, `parent list item not found: ${parentText}`);
  const nestedListStart = html.indexOf("<ul", parentIndex);
  assert.notEqual(nestedListStart, -1, `nested list not found under: ${parentText}`);
  const firstCloseAfterParent = html.indexOf("</li>", parentIndex);
  assert.notEqual(firstCloseAfterParent, -1, `parent list close not found: ${parentText}`);
  assert(nestedListStart < firstCloseAfterParent, `nested list was rendered outside parent list item: ${parentText}`);
  const childIndex = html.indexOf(childText, nestedListStart);
  assert.notEqual(childIndex, -1, `nested child text not found: ${childText}`);
  assert(childIndex < firstCloseAfterParent, `nested child was rendered outside parent list item: ${childText}`);
}
