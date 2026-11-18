// Minimal, dependency-free Markdown renderer for chat and reports.
import { JSX } from "react";

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function inline(s: string): string {
  let t = escapeHtml(s);
  // code spans first
  t = t.replace(/`([^`]+)`/g, (_m, c) => `<code>${c}</code>`);
  t = t.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  t = t.replace(/(^|[^*])\*([^*]+)\*/g, "$1<em>$2</em>");
  t = t.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_m, text, href) => {
    const safe = /^https?:\/\//.test(href) ? href : "#";
    return `<a href="${safe}" target="_blank" rel="noopener noreferrer">${text}</a>`;
  });
  return t;
}

export function Markdown({ text, streaming = false }: { text: string; streaming?: boolean }): JSX.Element {
  const html = render(streaming ? completeStreamingMarkdown(text) : text);
  return <div className="prose-chat" dangerouslySetInnerHTML={{ __html: html }} />;
}

// Complete only the unfinished tail for display; never change the stored reply.
// Fenced code is already handled by render, including an unclosed fence.
export function completeStreamingMarkdown(text: string): string {
  const lines = text.split("\n");
  let fenced = false;
  for (const line of lines) {
    if (line.trimStart().startsWith("```")) fenced = !fenced;
  }
  if (fenced) return text;
  const tail = lines[lines.length - 1];
  if (/^\s*(#{1,6}|[-*]|\d+\.)\s*$/.test(tail)) {
    return lines.slice(0, -1).join("\n");
  }
  // Do not expose a partially received link target to the renderer.
  let display = text.replace(/\[([^\]\n]*)\]\([^\)\n]*$/, "$1");
  const lastLine = display.slice(display.lastIndexOf("\n") + 1);
  if ((lastLine.match(/`/g) || []).length % 2) return display + "`";
  if ((lastLine.match(/\*\*/g) || []).length % 2) display += "**";
  return display;
}

export function render(src: string): string {
  const lines = src.replace(/\r\n/g, "\n").split("\n");
  const out: string[] = [];
  let i = 0;
  let inList = false;
  let listTag = "ul";
  const closeList = () => {
    if (inList) {
      out.push(`</${listTag}>`);
      inList = false;
    }
  };
  while (i < lines.length) {
    const line = lines[i];
    // fenced code
    if (line.trimStart().startsWith("```")) {
      closeList();
      const lang = line.trim().slice(3);
      const body: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trimStart().startsWith("```")) {
        body.push(lines[i]);
        i++;
      }
      i++;
      out.push(`<pre data-lang="${escapeHtml(lang)}"><code>${escapeHtml(body.join("\n"))}</code></pre>`);
      continue;
    }
    // headings
    const h = /^(#{1,6})\s+(.*)$/.exec(line);
    if (h) {
      closeList();
      const level = h[1].length;
      out.push(`<h${level}>${inline(h[2])}</h${level}>`);
      i++;
      continue;
    }
    // table (simple)
    if (/^\s*\|/.test(line) && i + 1 < lines.length && /^\s*\|?[\s:|-]+\|/.test(lines[i + 1])) {
      closeList();
      const header = splitRow(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && /^\s*\|/.test(lines[i])) {
        rows.push(splitRow(lines[i]));
        i++;
      }
      let t = "<table><thead><tr>";
      header.forEach((c) => (t += `<th>${inline(c)}</th>`));
      t += "</tr></thead><tbody>";
      rows.forEach((r) => {
        t += "<tr>";
        r.forEach((c) => (t += `<td>${inline(c)}</td>`));
        t += "</tr>";
      });
      t += "</tbody></table>";
      out.push(t);
      continue;
    }
    // list items
    const li = /^(\s*)[-*]\s+(.*)$/.exec(line);
    const oli = /^(\s*)\d+\.\s+(.*)$/.exec(line);
    if (li || oli) {
      const tag = oli ? "ol" : "ul";
      if (!inList || listTag !== tag) {
        closeList();
        listTag = tag;
        out.push(`<${tag}>`);
        inList = true;
      }
      out.push(`<li>${inline((li || oli)![2])}</li>`);
      i++;
      continue;
    }
    if (line.trim() === "") {
      closeList();
      i++;
      continue;
    }
    // horizontal rule
    if (/^---+$/.test(line.trim())) {
      closeList();
      out.push("<hr/>");
      i++;
      continue;
    }
    closeList();
    out.push(`<p>${inline(line)}</p>`);
    i++;
  }
  closeList();
  return out.join("\n");
}

function splitRow(line: string): string[] {
  return line
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((s) => s.trim());
}
