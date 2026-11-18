import type { AgentEvent } from "./api";

export interface LiveTool {
  callId: string;
  name: string;
  args: unknown;
  result?: string;
  isError?: boolean;
  running: boolean;
}
export type LiveBlock =
  | { kind: "text" | "reasoning" | "notice"; text: string }
  | { kind: "tool"; tool: LiveTool };

// Keep presentation in arrival order; finishing a tool updates its existing row.
export function reduceLive(blocks: LiveBlock[], ev: AgentEvent): LiveBlock[] {
  if (ev.type === "text" || ev.type === "reasoning" || ev.type === "notice") {
    if (!ev.text) return blocks;
    const last = blocks.at(-1);
    if (last && last.kind !== "tool" && last.kind === ev.type && ev.type !== "notice") {
      return [...blocks.slice(0, -1), { ...last, text: last.text + ev.text }];
    }
    return [...blocks, { kind: ev.type, text: ev.text }];
  }
  if (ev.type === "tool_start") {
    return [...blocks, { kind: "tool", tool: { callId: ev.call_id!, name: ev.tool!, args: ev.args, running: true } }];
  }
  if (ev.type === "tool_end") {
    return blocks.map((b) => b.kind === "tool" && b.tool.callId === ev.call_id
      ? { ...b, tool: { ...b.tool, result: ev.result, isError: ev.is_error, running: false } } : b);
  }
  return blocks;
}
