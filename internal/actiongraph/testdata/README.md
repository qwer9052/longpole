# Action graph fixtures

Captured from Go 1.27.1 windows/amd64 against a small module with four packages
plus the standard library. These are real output, not hand-written. Their value is
that they are not invented, so do not edit them by hand.

| File | What it captures |
|---|---|
| `cold.json` | Empty build cache. 195 of 392 actions ran. |
| `warm.json` | The same build repeated. 1 action ran. |
| `gotest.json` | `go test`, which adds vet and test-run action modes. |
| `failed.json` | A build with a compile error. The failing action has an `ActionID` but no `BuildID`. |
| `serial_p1.json` | Built with `-p=1`, so queue wait dominates. |
| `after_touch.json` | Rebuilt after editing one leaf package's source. |

The pair `cold.json` and `warm.json` is the important one: it is the same build
with an empty and a full cache, so any rule that fails to distinguish them is
wrong no matter how reasonable it looks. Measured across those two files:

| Field | Cold | Warm | Usable as a cache signal |
|---|---|---|---|
| `Cmd` / `CmdReal` | 195 | 1 | yes |
| `NeedBuild` | 195 | 195 | no |
| `Built` | 2 | 2 | no |
| `Failed` | 0 | 0 | no |

## Regenerating

In a scratch module:

```bash
go build "-debug-actiongraph=cold.json" -o app.exe .
```

The quotes matter on PowerShell, which otherwise splits the argument at `=`. They
are harmless on other shells.

For `warm.json`, run the same command again without changing anything. Use a
stable `-o` path rather than `/dev/null` or `NUL`, because with a null output the
link action re-runs every time and the build never looks fully cached.
