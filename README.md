# longpole

Why was my Go build slow? One command, honest numbers.

```
longpole go build ./...
```

Named for "the long pole in the tent" — the item that sets the schedule. That is
what this reports: the longest chain of dependent work in your build. No amount of
extra parallelism makes a build faster than its critical path.

> **Status: design complete, implementation not started.**
> The design and the measurements behind it are in
> [`docs/superpowers/specs/2026-09-12-longpole-design.md`](docs/superpowers/specs/2026-09-12-longpole-design.md).
> There is no installable binary yet.

## What it will report

```
  build: 392 actions, 195 ran, 197 cached (50%)        12.40s wall

  time went to
    compile      9.80s   79%   194 actions
    link         2.10s   17%   1 actions

  critical path  8.20s of 12.40s wall (66%)
      3.10s  k8s.io/client-go/kubernetes
      2.00s  example.com/app/internal/bigpkg
      + 12 more

  parallelism  1.9x of 8 cores  (work 23.10s / wall 12.40s)
    ! 4.20s queue wait across 38 actions — the graph is narrow here

  slowest packages
      2.00s  example.com/app/internal/bigpkg, blocks 47

  saved as run #7.  compare:  longpole diff 6 7
```

## Why this exists

The Go action graph does not say what it appears to say. There is no "cached"
field, and the three fields that look like one are traps. Measured on the same
build with an empty and a full cache:

| Field | Cold | Warm | Usable |
|---|---|---|---|
| `Cmd` / `CmdReal` | 195 of 392 | 1 of 392 | yes |
| `NeedBuild` | 195 | 195 | no |
| `Built` | 2 | 2 | no |
| `Failed` | 0 | 0 | no |

Tools that miss this rank cache probes as the slowest steps of a build where
nothing was rebuilt. longpole says what actually happened:

```
  build: 392 actions, 0 ran, 392 cached (100%)          0.50s wall
  nothing to optimize — everything came from cache
```

## What it measures

**Work time** is time actually spent in compiler and linker subprocesses. It is the
honest measure of cost, and it is what the rankings use. Cached actions contribute
zero, so they never appear in a "slowest" list.

**Queue wait** is time an action sat ready but unscheduled. High queue wait means
the dependency graph is too narrow to use your cores, or something is serializing
the build. No other tool reports this. It is the difference between blaming a
package and blaming the shape of your graph — golang/go#71981 is a real case where
40 seconds landed on one innocent package and the actual cause was lock contention.

**Critical path** is the heaviest chain of dependent work. Shortening anything off
the critical path does not make the build faster.

## Planned commands

| Command | What it does |
|---|---|
| `longpole go build ./...` | Profile a build |
| `longpole go test ./...` | Profile a test build |
| `longpole --explain go build ./...` | Also explain what rebuilt and why. Slower. |
| `longpole log` | List recent runs in this project |
| `longpole diff [A B]` | Compare two runs, defaulting to the last two |

## Requirements

Go 1.21 or newer. The action graph record shape was verified byte-for-byte
identical from Go 1.21 through 1.27.

longpole reads `go build -debug-actiongraph`, which is undocumented and unsupported
by the Go team. If a future release changes it, longpole warns and gets out of the
way: it never changes your build's output or exit code.

## Documentation

| Document | Contents |
|---|---|
| [Design spec](docs/superpowers/specs/2026-09-12-longpole-design.md) | The design and every measurement justifying it |
| [Implementation plan](docs/superpowers/plans/2026-09-12-longpole.md) | Task-by-task build plan |
| [Roadmap](docs/ROADMAP.md) | What is in v1, what is deferred, and why |
| [AGENTS.md](AGENTS.md) | Coding conventions, hard rules, debugging guide |

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
