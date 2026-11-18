import { describe, expect, test } from "bun:test";
import { completeStreamingMarkdown, render } from "../src/lib/markdown";

describe("streaming Markdown", () => {
  test("formats incomplete bold and inline code without changing source", () => {
    expect(render(completeStreamingMarkdown("**Hello"))).toContain("<strong>Hello</strong>");
    expect(render(completeStreamingMarkdown("Run `hello"))).toContain("<code>hello</code>");
  });
  test("does not modify open fenced code", () => {
    const text = "```js\nconst x = '**unfinished';";
    expect(completeStreamingMarkdown(text)).toBe(text);
    expect(render(text)).toContain("<pre");
  });
  test("hides incomplete block markers and link targets", () => {
    expect(completeStreamingMarkdown("Hello\n##")).toBe("Hello");
    expect(completeStreamingMarkdown("[Docs](https://exam")).toBe("Docs");
  });
  test("complete Markdown renders identically", () => {
    const text = "## Title\n\n**Hello** and `code`\n- One\n- Two\n\n| A | B |\n| --- | --- |\n| 1 | 2 |";
    expect(render(completeStreamingMarkdown(text))).toBe(render(text));
  });
  test("escapes untrusted markup during streaming", () => {
    expect(render(completeStreamingMarkdown("**<script>alert(1)</script>"))).not.toContain("<script>");
  });
});
