# AGENTS.md

longpole is a Go CLI that wraps `go build` / `go test` and reports why the build
was slow. Keep changes small and surgical. The tool reads undocumented Go
toolchain output, so most bugs here are wrong *interpretation*, not wrong code —
when in doubt, measure against a real build before changing a rule.

## Repo layout

- `cmd/longpole/` — argv dispatch and subcommand wiring only. No analysis logic.
  Treat it as a thin shell over `internal/`.
- `internal/actiongraph/` — decodes `go build -debug-actiongraph` JSON. Performs
  no interpretation. Every derived value belongs in `internal/model/`.
- `internal/actiongraph/testdata/` — real captured build graphs. Do not hand-edit
  them; regenerate instead (see the README in that directory).
- `internal/model/` — the only place that decides whether an action was cached.
- `internal/critpath/` — longest path and blast radius over the dependency DAG.
- `internal/hashlog/` — streaming parser for `GODEBUG=gocachehash=1` output.
- `internal/cause/` — rebuild cause analysis and causality chains.
- `internal/store/` — SQLite run history. Every failure here is non-fatal.
- `internal/report/` — pure rendering. Data in, string out, so output is golden-tested.
- `internal/wrap/` — subprocess wrapping, flag injection, stdio passthrough.

## Commands

```bash
go build ./...
go test ./...
go test ./... -race
go vet ./...
gofmt -l .
```

CI runs exactly these on `ubuntu-latest` and `windows-latest`, with two
differences worth knowing before you wonder why a check is missing:

- The build step sets `CGO_ENABLED=0`, which turns the no-cgo rule into a check.
- `-race` and `gofmt` run on Linux only. The race detector needs cgo, and a
  Windows runner would need a C toolchain for it; data races are not platform
  specific. Windows still runs build, vet and the full test suite, which is what
  catches the platform-specific behavior this tool depends on.

Locally on Windows, `go test ./... -race` fails with "requires cgo" unless you
have a C compiler. That is expected. Run the plain suite instead and let CI
cover the race detector.

Regenerate golden report files after an intentional output change:

```bash
go test ./internal/report/ -update
```

Read the regenerated files before committing them. They are the product.

Build and dogfood in one step:

```bash
go build -o longpole.exe ./cmd/longpole
./longpole.exe go build ./...
```

## Where to look first

- `docs/superpowers/specs/2026-09-12-longpole-design.md` — the design and every
  measurement that justifies it. Read this before changing any analysis rule.
- `docs/superpowers/plans/2026-09-12-longpole.md` — the implementation plan,
  including a "Critical domain knowledge" section that is required reading.
- `docs/ROADMAP.md` — what is in v1, what is deferred, and why.
- `internal/actiongraph/testdata/` — real captured build graphs, described in the README in that directory.

Code entry points:

- `cmd/longpole/main.go` — subcommand dispatch
- `internal/model/model.go` — `New()`, which holds the cache-hit rule
- `internal/wrap/wrap.go` — `Run()`, which owns the transparency contract

## Coding conventions

- Keep every change tied to the task. No drive-by cleanup, broad renames,
  formatting churn, or speculative abstractions unless the task asks for them.
- Standard library only, plus `modernc.org/sqlite`. Every new dependency needs a
  reason in the commit message.
- Comments explain *why*, not *what*. A comment restating the code is noise; a
  comment recording a measurement or a trap is the most valuable thing in the file.
- Errors wrap with `%w` and name the operation that failed: `fmt.Errorf("parse
  action %d: %w", i, err)`. A bare `return err` loses the only context the user gets.
- Use `context.Context` as the first argument for anything that does I/O or blocks.
- Do not add abstractions for one-off use cases. Add a helper only when it removes
  duplication across real call sites.
- Tests cover user-visible behavior and the boundary being changed: happy path,
  the empty or malformed input, and the specific trap the code exists to avoid.
- Table-driven tests where there is more than one case. Name subtests after the
  behavior, not the input.

## Hard rules and boundaries

These are load-bearing. Breaking one produces a tool that is confidently wrong,
which is worse than no tool at all.

1. **`Cmd == nil` is the only cache signal.** Never use `NeedBuild`, `Built`, or
   `Failed` to decide whether an action was cached. Measured on the same build,
   cold versus warm: `Cmd` 195 vs 1, `NeedBuild` 195 vs 195, `Built` 2 vs 2,
   `Failed` 0 vs 0. The go command writes the graph twice and the pre-execution
   write freezes those fields. This rule lives in `internal/model/model.go` and is
   pinned by dedicated tests. If you think you have a better signal, measure it on
   both fixtures first.

2. **longpole never changes a build's behavior.** The wrapped command's exit code,
   stdout, and stderr pass through unchanged. Analysis failures print one warning
   line and nothing else. If a change could make a passing build look failed, or
   swallow a compiler error, it is wrong.

3. **Never forward `HASH` lines to the user, and never swallow anything else.**
   Under `--explain` we set the GODEBUG that produces them, so we consume them.
   Every other stderr line is the build talking to the user. `IsHashLine` must stay
   strict: require the exact `HASH[`, `HASH subkey `, or `HASH <file>: <64 hex>`
   shapes.

4. **Never use `CmdUser` or `CmdSys`.** They come from `os.ProcessState` and have
   ~15.6 ms granularity on Windows, where measurements come back as literally
   `15625000` ns. Use `CmdReal`.

5. **`Objdir` is not a key.** It changes every invocation. `ActionID` is the stable
   identity across runs.

6. **Unknown action modes are not errors.** The go command has added modes before
   (`build check cache` arrived undocumented in Go 1.26) and will again. Classify
   unknown modes as "other" and keep going. Never panic, never fail the report.

7. **`built-in package` is neither cached nor rebuilt.** The `unsafe` package
   produces `NeedBuild: true`, `Cmd: nil`, zero duration. Counting it either way
   skews every report.

8. **Never rank cached actions by cost.** They did no work. Presenting cache probes
   as "slowest build steps" is exactly the bug that makes the existing tool
   misleading, and it is the reason this project exists.

9. **Never claim more than the data shows.** Causality is reported as a chain with
   its evidence, not as a verdict. Without a baseline, a rebuild root is "changed on
   its own", not a named file. Label hypotheses as hypotheses.

10. **Never buffer the `gocachehash` stream.** It is roughly a megabyte of stderr
    for a four-package module. Parse line by line and discard.

11. **No cgo.** longpole ships as a single cross-compiled binary. A cgo dependency
    would require a C toolchain per target.

12. **Do not hand-edit fixtures** in `internal/actiongraph/testdata/`. They are real
    captures and their value is that they are not invented. Regenerate them.

13. **Persistence is best-effort.** A build that cannot be recorded is still a build
    that succeeded. Store failures warn and the report still prints.

## Debugging

Before changing an analysis rule, reproduce against the fixtures. `cold.json` and
`warm.json` are the same build with an empty and a full cache, so any rule that
does not distinguish them is wrong regardless of how reasonable it looks.

| Symptom | Likely cause | Fix |
|---|---|---|
| Warm build shows packages under a "slowest" heading | A cached action leaked into a ranking | `TopByWork` must filter on `Ran`, not on `WorkNs > 0` alone |
| Cached and ran counts do not sum to the action count | Bookkeeping modes counted as work | Only `KindCompile` and `KindLink` are work; see rule 7 |
| A report number does not reconcile with wall time | Work time summed across parallel actions | Work exceeds wall on a parallel build. That is correct, not a bug |
| `-debug-actiongraph` argument reaches `go` mangled | PowerShell splits arguments containing `=` | Quote it: `go build "-debug-actiongraph=x.json"`. longpole itself uses `os/exec` and is immune |
| Link action re-runs on every build | Output is `/dev/null` or `NUL` | There is no target binary to compare build IDs against. Use a stable `-o` path |
| Compile errors vanish under `--explain` | `IsHashLine` is too greedy | Tighten it and add the swallowed line to its test |
| `ActionID` from a digest does not match the graph | Wrong base64 alphabet | It is `base64.URLEncoding` over the first 15 bytes, not `StdEncoding` |
| Every package rebuilt after a toolchain change | Expected | The Go version string salts every cache hash |
| CI never reuses a developer's cache | The absolute package directory is hashed in | `-trimpath` makes action IDs path-independent |
| Editing a package did not rebuild its dependents | Expected | A package's hash contains its dependency's *output content* ID. Unchanged output means dependents stay cached |

Rules for investigating:

- Reproduce before theorizing. The fixtures make most questions answerable without
  running a build.
- Label a guess as a guess. Do not present a code-path hypothesis as a confirmed cause.
- Prefer read-only checks. Do not clear the user's build cache or modify their
  checkout to collect evidence.
- Do not claim a fix works from compilation alone. Run the test that covers it and
  read the output.
- Quote the relevant excerpt, not a wall of output.

## Commit messages

Follow the Keycloak project's style, with two local differences noted at the end.

- One logical change per commit.
- Subject: a brief descriptive summary in sentence case. No trailing period.
- **No Conventional Commits prefixes.** Do not write `feat:`, `fix:`, `chore:`.
  An optional free-form scope is fine: `report: ...` or `[explain] ...`.
- Imperative or gerund mood. Never past tense.
  Good: `Fix queue wait on actions with a zero TimeReady`
  Good: `Stabilizing the golden report tests`
  Bad: `Fixed the queue wait bug`
- Keep it short. Aim for 30 to 70 characters. Most commits need no body at all.
- Add a body only when the *why* is not obvious from the subject. Wrap at 72
  columns.
- If the commit closes an issue, the last line of the body is `Closes #1234`.
  Never in the subject. Never lowercase variants.

Local differences from Keycloak:

- **Do not add AI or assistant attribution of any kind.** No `Co-authored-by`
  trailer naming a model or tool.
- No DCO sign-off is required. If a co-author trailer is genuinely needed, use
  `Co-authored-by: qwer9052 <namju@needsoft.co.kr>`.

## License

Apache License 2.0. `LICENSE` carries the full text and `NOTICE` names the
copyright holder; both ship in any distribution.

Apache 2.0 does not require a per-file header, and longpole does not use one —
169 lines of boilerplate repeated across a dozen small files buries the code that
matters. Do not add one. If a file is copied in from another project, keep its
original header and record the source in `NOTICE`.

## PR hygiene

- Branch from `main`.
- One issue per PR. Separate work gets a separate branch.
- Run the narrowest relevant test first, then `go test ./... -race`, `go vet ./...`
  and `gofmt -l .` before handing the PR over. All four must be clean.
- Do not claim a check passed without having run it and read the output.
- Explain intentional omissions in the PR body.
