# CLAUDE.md

Read and follow `AGENTS.md` for repository layout, commands, coding conventions,
hard rules, and debugging guidance.

The three rules violated most often are restated here because getting any of them
wrong produces a tool that is confidently wrong rather than merely broken.

## `Cmd == nil` is the only cache signal

The action graph has no "cached" field. `NeedBuild`, `Built` and `Failed` all look
like one and are not: measured on the same build, cold versus warm, `NeedBuild` is
195 and 195, `Built` is 2 and 2, `Failed` is 0 and 0. Only `Cmd` moves, 195 to 1.

Never trust those three fields. The rule lives in `internal/model/model.go` and is
pinned by tests that exist for no other purpose.

## longpole never changes a build's behavior

Exit code, stdout and stderr pass through unchanged. An analysis failure prints one
warning line and nothing more. If a change could make a passing build look failed,
or hide a compiler error, it is wrong no matter how good the report looks.

## Commit messages are short and carry no AI attribution

Sentence case, imperative or gerund, no `feat:`/`fix:` prefixes, 30 to 70
characters, usually no body. Never add a `Co-authored-by` trailer naming a model or
tool. See the "Commit messages" section of `AGENTS.md`.
