# Chat interaction regression checks

## Changes
- Streaming uses an arrival-ordered timeline: text, reasoning, notices, and tool rows. Tool completion updates its original row, including parallel calls.
- Saved assistant text is rendered before that step's requested tool calls.
- Composer Send becomes a red glowing Stop button while streaming; no duplicate Stop in the header.
- Thinking and active tool status use an animated four-point star. Reduced-motion preferences disable animation.
- Session list refreshes while a turn is active and consumes session_title events.
- Backend delivers interactive question/approval events into the active chat stream; generated titles use the selected chat provider.

## Verification
- Bun unit tests cover streaming order, text chunk coalescing, and out-of-order parallel tool completions, alongside Markdown regressions.
- Headless installed Chrome was driven using Playwright against the Vite UI with a mocked SSE/API. Verified actual element positions (text -> tool -> text), clicked a question choice and checked its answer API payload, checked automatic title refresh, Stop styling/location and API call, and presence of animated stars.
- UI test helper and screenshot: `.test-e2e/ui/chat-ui.cjs`, `.test-e2e/ui/chat-streaming.png` (local ignored artifacts).
- The browser test uses mocked model traffic; it does not claim a real vendor model selected a question tool.
