import { describe, expect, test } from "bun:test";
import { reduceLive, type LiveBlock } from "../src/lib/chat-stream";
import type { AgentEvent } from "../src/lib/api";

function replay(events: Partial<AgentEvent>[]) {
  return events.reduce((blocks, ev) => reduceLive(blocks, ev as AgentEvent), [] as LiveBlock[]);
}
describe("streaming chat timeline", () => {
  test("text before and after tools remains in separate ordered blocks", () => {
    const blocks = replay([
      { type: "text", text: "Before" }, { type: "text", text: " tool" },
      { type: "tool_start", call_id: "1", tool: "browser" },
      { type: "text", text: "After" },
      { type: "tool_end", call_id: "1", result: "ok" },
    ]);
    expect(blocks.map(b => b.kind)).toEqual(["text", "tool", "text"]);
    expect(blocks[0]).toEqual({ kind: "text", text: "Before tool" });
    expect(blocks[1]).toMatchObject({ tool: { result: "ok", running: false } });
    expect(blocks[2]).toEqual({ kind: "text", text: "After" });
  });
  test("parallel completions update original rows without moving them", () => {
    const blocks = replay([
      { type: "reasoning", text: "Plan" },
      { type: "tool_start", call_id: "a", tool: "grep" },
      { type: "tool_start", call_id: "b", tool: "read_file" },
      { type: "tool_end", call_id: "b", result: "second" },
      { type: "tool_end", call_id: "a", result: "first", is_error: true },
    ]);
    expect(blocks[1]).toMatchObject({ tool: { callId: "a", result: "first", isError: true } });
    expect(blocks[2]).toMatchObject({ tool: { callId: "b", result: "second" } });
  });
});
