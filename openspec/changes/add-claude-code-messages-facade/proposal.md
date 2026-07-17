# Add Claude Code Messages facade

## Why

Claude Code speaks the Anthropic Messages gateway protocol, while ya-router
currently exposes OpenAI-compatible surfaces. Operators need a local adapter
that preserves existing route selection, credential isolation, and no-replay
semantics.

## What changes

- Add the authenticated `/v1/messages` facade and connectivity probe.
- Translate supported Anthropic Messages requests to native Responses requests.
- Translate native Responses JSON and SSE output to Anthropic Message output.
- Add configured Claude discovery aliases without changing canonical OpenAI
  catalog IDs.
- Add bounded parsing, field/header policy, tests, fuzz coverage, and operator
  documentation.

## Non-goals

- Full Anthropic API emulation, synthetic thinking signatures, token counting,
  arbitrary beta-feature rewriting, or cross-provider replay.
