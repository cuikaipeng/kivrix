# Desktop functionality alignment

Kivrix targets the desktop workflows of [Tiny RDM](https://github.com/tiny-craft/tiny-rdm).
The comparison used Tiny RDM v1.2.7 and source revision
[`4ddb94a`](https://github.com/tiny-craft/tiny-rdm/tree/4ddb94a3c55f3401dbc66870f0d21cf0d6203cbc).
Web/Docker, its login/multi-user features, and compatibility with Tiny's
saved connection/data formats are outside the agreed scope. Kivrix keeps
its native MyGo UI; no Tiny source code or assets are incorporated.

## Implemented gap list

| Area | Implementation | Boundaries |
|---|---|---|
| Decode → edit → encode | Auto detection; formatted/decoded string and JSON editors; hash/list/set/zset row editing; built-in and custom encoders; TTL-preserving string save. | Ambiguous encodings can be selected explicitly. Custom editing needs an encoder. Pickle saving needs local Python. Serialization editors use JSON-compatible data. |
| Key browsing | Contains/glob/exact search, type filter, configurable namespace separator, tree/list view, per-connection default filter and scan size. | Automatically scans to completion in cancellable batches, within the key/tree memory limits. SCAN count is a hint, not an exact batch size. |
| Connections | Unix sockets, Redis URL form fill, connect/command timeouts, Sentinel master discovery, DB aliases and visibility, color markers, persistent empty groups and manual profile/group ordering. | Unix sockets are for local standalone connections. Ordering uses Move up/down actions. |
| Collections | Bulk add for hash/list/set/zset; list head/tail insertion preserving input order; sortable hash columns; list filtering past 10,000 entries; CSV export without the old 10,000-row cap. | Sorting applies to loaded rows. CSV is a text format; binary collection fields/values are rejected to prevent byte replacement. Transfers can be canceled. |
| CLI | Input history with Up/Down and draft restoration; SELECT switches the entire session and DB selector. | Streaming commands have dedicated pages. Transactions and authentication/protocol changes remain unavailable in the pooled CLI. |
| Preferences | Editor/CLI font and size, line numbers/folding/wrap, scan count, value page size and refresh interval. | Existing English/Chinese UI retained. |

These are the six implementation areas agreed for this pass. This is not
a claim that every menu, language or distribution detail matches Tiny.

## Existing desktop coverage

- Standalone, Sentinel and Cluster; SSH, TLS, HTTP CONNECT and SOCKS5.
- Multiple connection tabs, profile import/export without passwords,
  encrypted local credentials and connection testing.
- String, Hash, List, Set, ZSet, Stream and RedisJSON browsing/editing;
  key rename/delete, TTL, batch operations and namespace deletion.
- Incremental key/value loading, full key search, memory/length metadata,
  auto refresh, fullscreen values and a pinned row editor.
- Stream entry paging, consumer groups, consumers/pending entries,
  ACK/claim/auto-claim and trimming.
- Server status, client list, slow log, MONITOR, Pub/Sub and command log.
- Native settings, theme, shortcuts, command palette and signed in-app
  updates; desktop packaging workflows.

## Implementation limits

- RedisJSON editing is at the root. Stream pending entries show metadata;
  auto-claim processes one cursor batch per action. These are recorded
  implementation boundaries, not assumed missing Tiny functionality.
- Status history is an in-memory chart of the latest 40 samples. Cluster
  Status/Slow log/Monitor read one node.
- The UI currently supports English and Simplified Chinese, rather than
  Tiny's full language set. Shortcuts have no keybinding editor.
- Edited decoded data is bounded to 8 MiB, formatted data to 1 MiB, and
  BitSet to 64 KiB. Editable buffers refuse truncation.
- CSV has its own format and is not an RDB backup. Large values still need
  memory proportional to the exported value. Non-UTF-8 collection values
  are reported as skipped instead of being silently altered by JSON.
- Signing/notarization requires local or CI credentials. Local verification
  uses an ad-hoc signed macOS app, without publishing or installing it.

## Verification

```sh
CGO_ENABLED=0 go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go tool mygo vet ./internal/app
./scripts/integration.sh
go tool mygo build -platform darwin/arm64 -o build/parity-preview -sign - -skip-dmg -skip-notarize
```

The integration script starts disposable Redis standalone, Sentinel and
three-node Cluster instances. Coverage includes SCAN TYPE/exact matching,
encoded saves and TTL, Sentinel discovery, Unix socket commands/MONITOR,
DB switching and configured command deadlines. Unit/UI coverage includes
codec round trips and invalid input, bulk prevalidation, list search/CSV
past 10,000 rows, groups/order, CLI history and native view rebuilding.

Details: [feature reference](docs/FEATURES.md),
[packaging](docs/RELEASING.md).
