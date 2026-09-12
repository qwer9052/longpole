# longpole — design spec

Date: 2026-09-12
Status: approved, ready for implementation planning

## One-line pitch

Why was my Go build slow? One command, honest numbers.

```
longpole go build ./...
```

## Problem

Go gives no supported answer to "why was that build slow" or "why did that rebuild".
Issue golang/go#70691 ("I have no idea what the compiler is doing") was closed
not-planned. The three debug flags that expose the data are undocumented and
explicitly unsupported.

Teams work around this by hand. incident.io and howardjohn both wrote blog posts
about manually loading Go build traces into Perfetto, and both found the same
classes of problem: one oversized package serializing the whole action graph, a
single third-party dependency tree costing 67 seconds, and hundreds of test
binaries each being linked separately.

The only existing tool is `icio/actiongraph` (~135 stars, last commit 2024-02-22,
17 commits total). It sorts actions by `TimeDone - TimeStart` and nothing else.

## Why the incumbent is not good enough

Verified empirically against real build graphs on Go 1.27.1 / Windows 11.

On a **fully cached build where zero work happened**, `actiongraph top` reports:

```
0.275s   8.25%  link                example.com/lab/cmd/app
0.064s  15.86%  build check cache   internal/cpu
0.035s  16.92%  build check cache   internal/byteorder
```

It is ranking cache probes as "slowest build steps". It never says "everything
was cached, there is nothing to optimize."

Beyond that it: ignores `CmdReal`/`CmdUser`/`CmdSys` entirely (there is an
unimplemented TODO to that effect in its source at `main.go:102`); has no
cross-build comparison; parses `ActionID`/`BuildID` but never uses them; computes
no critical path despite rendering the DAG; and predates Go 1.26's
`build check cache` action split by two years.

## Verified technical ground truth

Everything in this section was reproduced on Go 1.27.1, Windows 11, against a
real module. Captured fixtures live in the research scratchpad and will be copied
into `testdata/`.

### Cache hit detection

There is no "cached" field. Three fields that look like they would work are traps.

`-debug-actiongraph` is written twice: once **before** execution (in case the
build fails early) and once after. Only fields mutated through the `a.json`
pointer are live in the final file. Everything else is frozen at graph
construction time.

Measured on one module, cold vs warm, 392 actions:

| Field | Cold | Warm | Usable |
|---|---|---|---|
| `Cmd` non-null | 195 | 1 | **yes** |
| `CmdReal` present | 195 | 1 | **yes** |
| `NeedBuild` | 195 | 195 | no |
| `Built` | 2 | 2 | no |
| `Failed` | 0 | 0 | no (always false; set during execution) |
| `ActionID` | 195 | 195 | stable key, not a hit signal |

**Rule:** an action is a cache hit when `Mode` is `build` or `link` and `Cmd` is
null (equivalently `CmdReal` is absent). Confirm secondarily with
`TimeDone - TimeStart` near zero.

### Schema stability

`actionJSON` in `cmd/go/internal/work/action.go` is byte-for-byte identical
across Go 1.21, 1.24, 1.25, 1.26 and 1.27. One parser covers all of them.

Fields: `ID, Mode, Package, Deps, IgnoreFail, Args, Link, Objdir, Target,
Priority, Failed, Built, VetxOnly, NeedVet, NeedBuild, ActionID, BuildID,
TimeReady, TimeStart, TimeDone, Cmd, CmdReal, CmdUser, CmdSys`. There are no
others. `Link` is dead code, never assigned anywhere in `cmd/go`.

One behavioral change: Go 1.26 added a `build check cache` action mode that
splits the cache probe out of the build action. It doubles the action count and
carries no `ActionID`. It is undocumented in the release notes. The parser must
handle both shapes.

### ActionID stability

`ActionID` is deterministic and identical across separate `go build` invocations
on the same machine. It changes with: source content, Go version (the version
string salts every hash), `GOOS`, `GOARCH`, `GOAMD64`, `-trimpath`,
`-gcflags=all=...`, and **the absolute package directory**.

It does *not* change with: `-ldflags` (link-only), `-tags` that do not change file
selection, `-gcflags` without `all=`, or `CGO_ENABLED` on a package with no cgo
files.

Two consequences that shape the product:

1. A CI checkout at a different path shares nothing with a developer's cache
   unless `-trimpath` is used. With `-trimpath`, ActionIDs are path-independent
   and portable across machines. This is worth telling users.
2. **A source change does not automatically invalidate dependents.** Verified:
   editing `lab/a` changed `a`'s ActionID and rebuilt it, but `lab/b` stayed
   cached, because `b`'s hash contains `a`'s *output content* ID, which was
   unchanged. Users consistently get this wrong, and explaining it is a
   significant part of the tool's value.

### GODEBUG=gocachehash=1

Writes the complete ordered input stream of every cache hash to stderr, followed
by the final digest:

```
HASH[build example.com/lab/a]: "go1.27.1"
HASH[build example.com/lab/a]: "compile\n"
HASH[build example.com/lab/a]: "dir C:\\...\\lab\\a\n"
HASH[build example.com/lab/a]: "goos windows goarch amd64\n"
HASH[build example.com/lab/a]: "GOAMD64=v1\n"
HASH[build example.com/lab/a]: "file a.go 9SqAMQcEh9zzRXLLCibe\n"
HASH[build example.com/lab/a]: "import fmt 6yHny8j1aq7yvW1BoyvM\n"
HASH[build example.com/lab/a]: 2c9680efc7e3447cab20e45e155b992b21218bd63e30939919b458a857ae4803
```

Diffing these blocks between two runs yields a precise, human-readable cause.
Also emits `HASH subkey <parent> <desc> = <out>` and `HASH <file>: <sha256>`.

Cost: 962 KB of stderr for a 4-package-plus-std build, written unbuffered one
`fmt.Fprintf` per write. It will meaningfully slow large builds. Must be
stream-parsed, never buffered, and must be opt-in.

Values are `%q`-quoted Go strings; parse with `strconv.Unquote`.

This GODEBUG is undocumented and carries no compatibility guarantee.

### Joining ActionID to the digest

`ActionID` in the action graph is the base64url encoding of the **first 15 bytes**
of the same SHA-256 that `gocachehash` prints. Verified: digest
`2c9680efc7e3447c...` maps to ActionID `LJaA78fjRHyrIOReFVuZ`. This is what lets
the hash-input diff attribute to both a package and its wall-clock cost.

### Queue wait

`TimeStart - TimeReady` is the time an action sat ready but unscheduled. No
existing tool surfaces it, and it is free in the graph.

It matters. golang/go#71981 reported a Windows build taking 40 seconds, with
`build internal/godebugs` absorbing 40.348s (99.17%) of it. The real cause was
lock contention in tool-ID computation, not that package. A tool reporting only
wall clock per action would have blamed the wrong thing. Reporting work time
(`CmdReal`), wall time, and queue wait separately localizes it correctly.

### Windows specifics

- `-debug-actiongraph` works. All of this research was done on Windows.
- `CmdUser`/`CmdSys` come from `os.ProcessState` and have ~15.625 ms granularity
  on Windows. Measurements come back as literally `15625000` ns. **Use `CmdReal`
  only.**
- `Objdir` and `Cmd` use backslashes, JSON-escaped. `Built`/`Target` may use
  forward slashes. Normalize.
- `Cmd` strings are produced by `joinUnambiguously`, which quotes any argument
  containing a space or `(`, `)`, `>`, `;`. On Windows nearly every path is
  quoted. Parse with a Go-string-aware splitter, not `strings.Fields`.
- Timestamps carry a local offset, not UTC.

### -o /dev/null caveat

`-o /dev/null` (and `NUL` on Windows) is special-cased and works, but the link
action re-runs on every invocation because there is no target binary to compare
build IDs against. Measured: rebuilding to a stable `-o` path with nothing
changed ran 0 of 196 subprocesses; with `-o NUL` it ran 1 of 195. When longpole
needs to force a build for analysis, it must use a stable temp output path, not
`/dev/null`.

## Scope

### In scope for v1

- Wrapping `go build` and `go test`
- Terminal output only
- Persistent run history in SQLite
- Cross-run diff
- `gocachehash` root-cause explanation, opt-in via `--explain`

### Out of scope for v1

- HTML reports, TUI, Perfetto export
- GOCACHEPROG interception (verified to work, but a cacheprog fully *replaces*
  the disk cache rather than filtering it, so a recorder must reimplement
  storage; too much risk for v1)
- `-debug-trace` consumption (complementary but redundant with the action graph
  for the headline question)
- Remote cache, CI dashboards, PR comments

## Command surface

```
longpole go build ./...              wrap, run, report
longpole go test ./...               same
longpole --explain go build ./...    also record hash inputs (slower)
longpole log                         list recent runs
longpole diff [A] [B]                compare two runs
longpole why <package>               why did this package rebuild
```

`longpole <anything>` where the first argument is `go` is treated as a wrap.
Everything else is a subcommand. This keeps the common case free of ceremony.

## Output

### Slow build

```
  build: 392 actions, 195 ran, 197 cached (50%)        12.4s wall

  time went to
    compile   9.8s   79%   194 actions
    link      2.1s   17%   1 action
    cache     0.5s    4%   194 probes

  critical path  8.2s of 12.4s wall (66%)
    3.1s  k8s.io/client-go/kubernetes
    2.0s  example.com/app/internal/bigpkg
    1.4s  google.golang.org/protobuf/proto
    + 12 more

  parallelism  1.9x of 8 cores  (work 23.1s / wall 12.4s)
    ! 4.2s queue wait across 38 actions — graph is narrow here

  biggest package
    example.com/app/internal/bigpkg  2.0s, 84 files, blocks 47 packages

  saved as run #7.  compare:  longpole diff 6 7
```

### Fully cached build

```
  build: 392 actions, 0 ran, 392 cached (100%)          0.5s wall
  nothing to optimize — everything came from cache
  (0.35s cache probing, 0.15s go command overhead)
```

This is the case the incumbent gets wrong, so it is a headline example in the
README.

### diff

```
  run 6 -> run 7        8.1s -> 12.4s   (+4.3s)

  rebuilt this time, cached last time     53 packages, +4.1s
    2.0s  example.com/app/internal/bigpkg
    0.9s  example.com/app/api
    + 51 more

  root cause  (1 package changed on its own; 52 followed)
    example.com/app/internal/config
      file config.go changed
      -> 3 direct dependents -> 52 transitive
```

The root-cause block appears when hash-input data exists for both runs. Without
it, the diff still reports what rebuilt and what it cost.

### why

```
  longpole why example.com/app/api

  last build: rebuilt, 0.9s
  ActionID   Ncj38CzRJXZZ4v3xvrBq  (was LJaA78fjRHyrIOReFVuZ in run 6)

  changed input
    import example.com/app/internal/config   content changed

  chain
    config  (own source changed: config.go)
      -> api
      -> 51 more downstream
```

## Causality without a baseline

The hash-input diff needs a previous run to compare against. To stay useful on
the very first `--explain` run, longpole computes the causality chain **within
a single build**.

A package's hash inputs contain `import <path> <contentID>` for each dependency.
If a dependency rebuilt in this same run and its content ID is one of the changed
inputs, causality is established locally. Walking that relation to its root
yields a chain ending at packages whose own `file <name> <hash>` or configuration
lines are responsible.

Without a baseline the root is reported as "own source or configuration changed".
With a baseline it is upgraded to the exact line, for example "file config.go
changed" or "GOAMD64 changed". Baselines accumulate automatically from every
`--explain` run.

## Architecture

| Package | Responsibility | Depends on |
|---|---|---|
| `cmd/longpole` | CLI dispatch, subprocess wrapping, flag injection, signal passthrough | model, store, report |
| `internal/actiongraph` | Parse the JSON graph. Handles Go 1.21-1.27 including the 1.26 `build check cache` split. Path normalization. | stdlib only |
| `internal/hashlog` | Stream-parse `gocachehash` stderr into per-block ordered input lines. Never buffers. | stdlib only |
| `internal/model` | `Run`, `Action`. Derives work time, wall time, queue wait, cached flag, kind. The single place the cache-hit rule lives. | actiongraph |
| `internal/critpath` | Longest-path over the Deps DAG weighted by action duration. Also fan-out/blast-radius counts. | model |
| `internal/cause` | Hash-input diffing and causality chain construction. | model, hashlog |
| `internal/store` | SQLite persistence, run retention, string interning for hash inputs. | model |
| `internal/report` | Terminal rendering. Pure functions from model to string. | model, critpath, cause |

Each unit is testable without running a build. `report` takes data and returns
text, so every output shape is a golden-file test.

## Data flow

1. Parse argv. If the first argument is `go`, this is a wrap.
2. Create a temp file for the action graph. Inject
   `-debug-actiongraph=<temp>` into the argument list after the go subcommand.
3. If `--explain`, set `GODEBUG=gocachehash=1` in the child environment and
   attach a stderr tee that stream-parses hash blocks while passing bytes
   through unchanged.
4. Run `go` with stdin, stdout and stderr connected. Forward signals.
5. On exit, capture the go command's exit code.
6. Parse the graph. Compute metrics. Persist the run.
7. Print the report to stderr (so it never pollutes a piped stdout).
8. Exit with the go command's exit code.

## Transparency rules

The wrapper must never be the reason a build behaves differently.

- The go command's exit code is propagated exactly.
- stdout and stderr pass through byte-for-byte. The report goes to stderr after
  the child exits, so it never pollutes a piped stdout and never interleaves
  with build output.
- Under `--explain` the child's stderr carries the `gocachehash` stream. Those
  lines are consumed by the parser and **not** forwarded, because they are
  diagnostic noise the user did not ask to see. Every other stderr line is
  forwarded unchanged. A line is treated as hash output only if it matches the
  `HASH[`, `HASH subkey `, or `HASH <file>: <64 hex>` shapes exactly; anything
  else passes through. This is the one place longpole alters child output, and
  it does so only for output it caused by setting the GODEBUG itself.
- Signals (Ctrl-C) are forwarded to the child; longpole waits for it to exit.
- If analysis fails for any reason, print a one-line warning and still exit with
  the child's code. A broken profiler must not break a build.
- If the user already passed `-debug-actiongraph`, honor theirs and read that
  file instead of injecting a second one.
- If the build failed, the graph still exists and is still analyzed. The report
  notes the failure and reports what did run.

## Storage

Location: `os.UserCacheDir()/longpole/runs.db`. SQLite via `modernc.org/sqlite`
so there is no cgo and cross-compilation stays trivial.

Tables:

- `runs` — id, timestamp, command, args, go version, GOOS, GOARCH, GOMAXPROCS,
  module path, working directory, wall ns, work ns, actions ran, actions cached,
  exit code
- `actions` — run id, action id, mode, package, work ns, wall ns, queue ns,
  cached, dep count
- `hash_inputs` — run id, block key (package + mode), sequence, string id
- `strings` — id, text. Hash input lines repeat heavily across packages, so
  interning them keeps the database small.

Runs are scoped by module path plus working directory so `diff` never compares
unrelated projects. Retention defaults to the last 50 runs per scope, pruned on
write.

Database failures are non-fatal. If the store cannot be opened or written, the
report is still printed and a warning is emitted.

## Error handling

| Condition | Behavior |
|---|---|
| `go` not on PATH | Clear error before doing anything else. Exit 1. |
| Go version below 1.21 | Refuse with an explanation. The flag predates 1.21 but the schema was not verified there. |
| Go version above 1.27 | Warn that the schema is unverified, attempt anyway. |
| Graph file missing or empty | The build probably failed before writing. Report the exit code, skip analysis, warn. |
| Graph fails to parse | Warn with the offending offset, skip analysis, preserve the exit code. |
| SQLite unavailable or locked | Warn, print the report, skip persistence. |
| `diff` with fewer than two stored runs | Explain how to produce more. |
| `why <pkg>` with no stored data | Explain that `--explain` must be run at least once. |

## Testing

Real fixtures already exist from the research phase and go into `testdata/`:
cold build, warm build, `go test`, `-p=1`, `-o NUL`, and a failed build, all
captured from Go 1.27.1 on Windows.

- `actiongraph` — golden tests over every fixture. Assert the 1.26 cache-split
  shape parses. Assert Windows path and quoted-command handling.
- `model` — the cache-hit rule gets dedicated tests asserting that `NeedBuild`,
  `Built` and `Failed` are *not* used. This is the thing most likely to be
  broken by a well-meaning future edit, so it is pinned by test.
- `critpath` — synthetic DAGs with known longest paths, including diamonds, and
  a wide-but-shallow graph that should report high parallelism.
- `hashlog` — parse a captured `gocachehash` stream. Assert quoted-string
  handling and that memory stays flat on a large input.
- `cause` — two captured runs with one known source edit. Assert the root is
  identified and the chain length is right.
- `report` — golden files for slow, fully cached, failed, and diff outputs.
- End-to-end — build a tiny module twice in a temp GOCACHE, assert the first run
  reports work and the second reports fully cached.

## Risks

| Risk | Mitigation |
|---|---|
| `-debug-actiongraph` is undocumented and unsupported | Schema verified identical across 1.21-1.27. Version-gate, warn outside the tested range, and fail soft. |
| `GODEBUG=gocachehash=1` is undocumented | Opt-in only. Failure to parse degrades to the no-baseline causality chain. |
| `gocachehash` output volume on large repos | Stream-parse, intern strings, never buffer. Measure on a large open-source repo before release. |
| Go adds a first-party answer | Unlikely near-term: #70691 was closed not-planned and 1.25 through 1.27 added nothing for build observability. |
| Causality heuristic is wrong | Report it as a chain with explicit evidence lines rather than a verdict. Never claim more than the data shows. |

## Success criteria for v1

- Correctly reports "nothing to optimize" on a fully cached build, which the
  incumbent cannot do.
- On a real open-source monorepo, identifies the critical path and the top cost
  packages, and the numbers reconcile with total wall clock.
- `diff` across a known one-file edit names the right root package.
- Adds under 200 ms of overhead to a build without `--explain`.
- Never changes a build's exit code or output.

## Name

`longpole`, after "the long pole in the tent" — the item that determines the
schedule. That is exactly what the tool finds and reports: the critical path.

Chosen 2026-09-12, replacing the working name `gobuildwhy`. Checked free on both
GitHub and pkg.go.dev at that date. Several obvious alternatives were already
taken in adjacent domains and were rejected for that reason: `tach` (2.8k-star
Python dependency tool), `caliper` (Google's Java benchmarking library),
`whyslow` (an existing profiling utility), `buildprof` (a build file-access
recorder), `hitch`, `tock`.

Two properties drove the choice. The binary is typed as a prefix on every build,
so it must be short and must not repeat the word "build". And a distinctive name
is findable among the many generically named build tools, where `buildwhy` or
`buildtime` would not be.
