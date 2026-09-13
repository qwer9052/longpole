# longpole

Why was my Go build slow? One command, honest numbers.

```
longpole go build ./...
```

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

Named for "the long pole in the tent" — the item that sets the schedule. That is
what this reports: the longest chain of dependent work in your build. No amount
of extra parallelism makes a build faster than its critical path.

## Why not just read the action graph yourself

Because the action graph does not say what it appears to say. There is no
"cached" field, and the three fields that look like one are traps: `NeedBuild`,
`Built` and `Failed` measure identically on a cold build and a fully cached one.
The only reliable signal is whether a subprocess ran.

Tools that miss this rank cache probes as the slowest steps of a build where
nothing was rebuilt. longpole says what actually happened:

```
  build: 392 actions, 0 ran, 392 cached (100%)          0.50s wall
  nothing to optimize — everything came from cache
```

## Commands

| Command | What it does |
|---|---|
| `longpole go build ./...` | Profile a build |
| `longpole go test ./...` | Profile a test build |
| `longpole --explain go build ./...` | Also explain what rebuilt and why. Slower. |
| `longpole log` | List recent runs in this project |
| `longpole diff [A B]` | Compare two runs, defaulting to the last two |

## What it measures

**Work time** is time actually spent in compiler and linker subprocesses. It is
the honest measure of cost, and it is what the rankings use. Cached actions
contribute zero, so they never appear in a "slowest" list.

**Queue wait** is time an action sat ready but unscheduled. High queue wait means
the dependency graph is too narrow to use your cores, or something is
serializing the build. No other tool reports this, and it is the difference
between blaming a package and blaming the shape of your graph.

**Critical path** is the heaviest chain of dependent work. Shortening anything
off the critical path does not make the build faster.

## Requirements

Building or installing longpole from source requires Go 1.25 or newer.
longpole supports profiling Go toolchains from Go 1.21 onward; that action-graph
format is verified through Go 1.27.

longpole reads `go build -debug-actiongraph`, which is undocumented and
unsupported by the Go team. The record shape has been identical since Go 1.21.
If a future release changes it, longpole warns and still gets out of the way:
it never changes your build's output or exit code.

## Install

```
go install github.com/qwer9052/longpole/cmd/longpole@latest
```

## Known limitations

- The Go version check uses the toolchain that built longpole, not the `go` on
  your PATH. They are usually the same.
- Wall time is measured around the `go` process, so it includes go command
  startup that the action graph does not attribute to any action.
- `--explain` makes the go command print a large volume of diagnostic output.
  Expect builds to take noticeably longer under it.

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
