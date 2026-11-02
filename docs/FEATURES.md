# Feature reference

This is the detailed behavior reference for Kivrix. For the project
overview, installation, licensing and acknowledgements, see the
[README](../README.md) and the [Chinese README](../README.zh-CN.md).

## Connections

- Saved profiles in `profiles.json` under the app's data directory;
  passwords live in the encrypted local vault (`redis`, `ssh`,
  `ssh-passphrase`, `sentinel` and `proxy` entries), never in the
  profile file.
- Several connections open at once, each in a tab in the title bar.
- Optional Development, Testing and Production badges identify the environment.
  Read-only connections disable writes in the editor, imports, CLI and server
  tools, including pipelines and cluster node operations. Unknown commands,
  scripts and unrecognized module commands are rejected in read-only mode.
  This is a client-side guard against mistakes; Redis ACLs control server access.
- The connection form keeps common fields visible and groups transport and
  tuning options under Advanced settings. Connect, Save and Test connection
  remain fixed below the scrolling form.
- The editor splits the server address into Host and Port inputs; saved
  profiles, imports and older `host:port` values are converted both ways.
  **Test connection** opens a throwaway connection with the form's current
  settings and credentials, reports success or the error, and saves
  nothing.
- Double-click a saved connection to connect. Its password is read from
  the local vault on the first connect, then cached for the rest of the
  run, so later double-clicks do not ask again. Selecting a profile alone
  reads no secrets.
- Saved connections are grouped by their `group` field, with collapsible
  sections, a name/address/group filter, duplicate and delete actions,
  group rename/remove, and Move up/down actions for connections and groups.
  New group also creates an empty group, which persists across restarts.
  Connections can have a color marker.
- Connection profiles can be exported to and imported from JSON. Passwords
  are never included, so they must be entered again after importing;
  imported profiles get fresh IDs and invalid entries are skipped.
- "Disconnect all" closes every open session from the connection menu.
- Connection modes:
  - **Standalone** to one Redis server.
  - **Sentinel** with one or more sentinel addresses, a master name, and
    optional sentinel ACL credentials. Failover clients follow the
    current master. Discover lists master names using the current Sentinel
    credentials and transport settings.
  - **Cluster** with one or more seed nodes. Keys are scanned across the
    masters; keyspace counts are summed; Flush DB reaches every master;
    multi-key deletes and renames are split or copied so they do not hit
    CROSSSLOT. A cluster has only `db0`, so the DB selector is disabled.
- Switch standalone/sentinel databases from the key sidebar; the selector
  lists the server's databases and the current one's key count. Profiles
  can assign database aliases and choose which DB numbers to show or hide.
  The active DB always remains visible.
- Paste a `redis://`, `rediss://` or `unix://` URL to fill the connection
  form. Credentials still go into the encrypted vault. Local standalone
  connections can use a Unix socket without SSH or a proxy.
- Advanced settings include connect/command timeouts, a default key glob,
  namespace separator, default tree/list view and a per-connection scan
  size. Zero uses the default timeout or the global scan preference.
- TLS with an optional CA bundle, client certificate/key pair, SNI/server
  name override, and an explicit “Skip TLS verification” opt-out. The
  same TLS settings cover every connection the mode opens, including
  sentinel hops and cluster nodes.
- HTTP CONNECT or SOCKS5 proxy, with username/password auth. The proxy
  sits in front of Redis, sentinel and cluster connections; SSH can run
  through it too.
- SSH tunnel with password, private key, private-key passphrase, or the
  running SSH agent (`SSH_AUTH_SOCK`). SSH host keys are checked against
  `~/.ssh/known_hosts`; tick “Skip host key check” for a host you have
  not connected to from a terminal yet.

Cluster caveat: Status and Slow log read one cluster node, and Monitor
streams one seed node. Pub/Sub uses the cluster client. Because
`RENAME` cannot cross hash slots, a cluster rename copies the serialized
value and removes the old key rather than being a single atomic command.

## Keys

- SCAN the keyspace in a namespace tree or a flat list, one page at a
  time. The separator defaults to `:` and is configurable per connection.
  Search supports contains, Redis glob patterns and exact key names,
  including literal wildcard characters in exact mode. A type filter can
  restrict results to one Redis type. Load more/all continue from the
  cursor. The footer distinguishes loaded keys from a completed scan; Cancel
  keeps already loaded keys. Resource limits still apply (50,000 loaded keys
  and a 16 MiB estimated tree budget).
- Browse string, hash, list, set, zset, stream and RedisJSON values.
  Large collections default to 200 rows per page with Load more; the page
  size is configurable. Hash fields, set members, sorted-set members and
  list elements have a filter. List search scans forward in chunks and
  keeps the original element index, including matches after 10,000 rows.
- Add, edit and delete rows of a hash, list, set or sorted set. Double-
  click a row or use its context menu; the toolbar's plus adds one or a
  batch. Bulk input uses one value per line, `field=value` for hashes or
  `member=score` for sorted sets. The entire batch is validated and encoded
  before sending it. Lists can insert at the head or append at the tail;
  input order is preserved. Hash field/value headers sort loaded rows.
  Streams show the newest 200 entries with their ID, fields, last ID and
  group count; the plus appends an entry and the row menu deletes one.
  Consumer groups can be listed, created at `$`/an ID and destroyed from
  the Groups button. Selecting a group shows its consumers and pending
  entries; consumers can be deleted, pending rows can be acknowledged or
  claimed, and Auto-claim runs XAUTOCLAIM with a minimum idle time.
  Pending entries load in pages of 200. Destroying a group asks for
  confirmation. Streams can be trimmed by MAXLEN or MINID, optionally
  approximately. Entries have a filter and Load more walks older entries
  with `XREVRANGE`; the newest page is still 200 entries.
- RedisJSON values open in the JSON editor at the root; Save uses
  `JSON.SET`, and the New Key dialog can create JSON keys. The RedisJSON
  module must be loaded.
- Check mode turns keys into checkboxes for a persistent selection, with
  select-all for the loaded tree and the same batch TTL/delete actions.
  Full search loads every page matching the current key filter and
  reports the result count.
- The value header can copy the raw value, hide the sidebar for a
  fullscreen view, and auto refresh the current value and database
  counts at the configured refresh interval (five seconds by default).
  Auto refresh pauses when the string/JSON
  editor has unsaved changes or a row editor is open. Binary key names
  show a BIN badge and cannot be renamed, while copy/TTL/delete remain
  available.
- Show values as Auto, Raw, JSON, JSON Unicode, YAML, XML, Hex, Binary or
  BitSet, and decode Auto, Base64, Base64 URL, GZip, Deflate, Zlib, ZStd,
  LZ4, Brotli, Msgpack, PHP, Pickle, Hex or URL before formatting. Auto uses
  recognizable signatures and conservative text detection; ambiguous
  values can use an explicit decoder.
- Formatted and decoded string/JSON values and hash/list/set/zset row
  values are editable. Save reverses the format and restores the encoding;
  strings keep their TTL. Original member identities are retained when
  replacing encoded set/zset members. Raw is also available explicitly.
  Msgpack, PHP and Pickle editors use JSON-compatible values. Pickle saving
  requires a local Python installation; decoding does not.
- Editable buffers reject truncation: decoding is limited to 8 MiB, final
  editor text to 1 MiB and BitSet input to 64 KiB. Binary content can be
  edited as Hex/Binary. Invalid JSON or encoded input does not write to
  Redis.
- Named custom decoder commands can be managed in Preferences. A command
  is split into arguments without a shell; the raw value is written to
  stdin; `{VALUE}` arguments are rejected to keep values out of process
  listings. Commands have a 10-second timeout, an 8 MiB output cap and a
  shared two-process concurrency limit. They are not sandboxed and must be
  trusted. On macOS/Linux, cancellation also terminates their process group.
  A decoder can also have an optional encode command. When it does,
  string/JSON and collection row editors can save through that encoder,
  including formatted views. A decoder without an encoder is read-only.
  Formatted collection previews run in the background and are cached for the
  selected key and format.
- Create string, JSON, hash, list, set and zset keys, rename keys, set
  or remove a TTL, delete selected keys in a batch, flush the current
  database, and delete whole namespaces after a confirmation. Deleting a
  namespace removes every key with that prefix on the server, not only
  the keys already scanned.
- Unsaved string/JSON edits are marked in the header and footer. Switching
  keys or databases, rescanning, reloading, disconnecting and closing the window
  offers Save and continue, Discard or Cancel. A failed save keeps the draft;
  a successful save continues only if no newer edits appeared. Save is disabled
  when there are no changes.
- Strings over the 1 MiB editor limit show a read-only preview of the first
  64 KiB, with the full byte size. Copy preview copies only that prefix; the
  preview cannot be saved. Other value and export limits remain in effect.
- Destructive confirmations identify the connection, DB and environment.
  Flush requires typing the DB number (for example `db0`); namespace deletion,
  deletion of ten or more keys, and multi-key deletion in Production require
  typing `DELETE`. Namespace deletion uses the connection's configured separator.
- Edit string and JSON values in the code editor. The card footer shows
  the value's total length and `MEMORY USAGE` when the server supports it.
- The row editor's pin button keeps the dialog open after Save, for
  consecutive edits to one field/member/element.
- Export the selected keys, or every loaded key, from the session menu to
  a CSV file, and import one back. The columns are `key,type,ttl,value`;
  collection values are JSON in the value column. There is no 10,000-row
  collection cap. Collections with non-UTF-8 fields or values are skipped
  with an error, because JSON would alter their bytes. CSV transfer runs
  in the background with processed/skipped counts, elapsed time and Cancel.
  Progress updates are coalesced to at most ten per second. Completed rows
  remain in a partial export; canceling import or namespace deletion leaves
  completed writes/deletions in place. The import dialog can
  skip or overwrite existing keys, include the CSV TTL, ignore it or set
  a custom TTL, and reload the keyspace afterwards. The export dialog can
  omit TTLs. Overwrite validates the row before deleting the old key. The
  CSV contains values in plain text, not encrypted.

## Preferences and shortcuts

- The sidebar can be resized or collapsed (`Cmd+B` / `Ctrl+B`); its width and
  visibility are remembered. Smaller windows constrain the sidebar to keep
  the working card usable, and the tool strip scrolls horizontally.
- The command palette searches actions, saved connections and the 20 most
  recent keys. Recent keys retain their connection and DB and stay in memory
  only. Use Up/Down to select, Enter to open and Escape to close. At most 40
  results are rendered; type more to narrow the list.
- General → Startup → Open on launch chooses the connection list (default)
  or the last active profile and DB when the app starts.
- General also configures SCAN count, collection page size, refresh
  interval, editor/CLI fonts and sizes, line numbers, folding and soft wrap.
- `preferences.json` next to `profiles.json` stores the appearance
  (Follow System/Light/Dark), background wash, translucency, language
  (Follow System/English/简体中文), default value format and decoder for new sessions, named custom decoder
  commands, and whether to check for updates on launch (enabled by default).
- General settings, the top-left connection menu and the native application
  menu offer manual update checks. New versions appear at the main window's
  top right. Downloading verifies and prepares a signed update; **Install and
  restart** activates it with protection for unsaved edits. Self-updating
  requires a packaged app in a writable location; Linux DEB installations
  use their package manager.
- The settings button opens a separate native Settings window: a
  navigation rail over the app wash on the left, a white card with the
  page on the right, and native window controls instead of an in-content
  close button. Appearance follows kero: theme, language, translucent
  window with adjustable background transparency (0–100%, default 40%)
  and ten background washes (Petal, Mist, Meadow, Dusk, Tide, Apricot,
  Aurora, Dune, Paper, Plain). The slider
  previews both windows immediately and saves automatically; text and
  content cards stay opaque. The wash fades smoothly to a subtle 25% tint
  at the slider's maximum, preserving the selected colors. On macOS, the
  system's blurred under-window material follows the selected light/dark
  appearance. Platforms without a blurred material use an opaque background.
  Decoders manages custom commands; Shortcuts lists the fixed keymap.
- The GitHub release check can download the matching asset, verify its
  SHA-256 digest and open the installer. It does not silently replace the
  running app.
- Shortcuts: `Cmd+K` or `Cmd+Shift+P` opens the command palette, `Cmd+,`
  Preferences, `Cmd+N` New Connection, `Cmd+R` rescan, `Cmd+F` focus the
  key filter, `Cmd+S` save a string/JSON value, `Cmd+B` toggle the sidebar,
  `Cmd+1…7` switch tool
  pages, and `Cmd+Shift+W` closes the active connection. On the connection
  screen, `Cmd+Up`/`Cmd+Down` move the selection and `Cmd+Enter` connects;
  `Ctrl+Tab` and `Ctrl+Shift+Tab` cycle open connection tabs.
- The command palette filters commands by name and runs the first result
  on Enter.
- Chinese translation covers the main shell, tool pages, status metrics,
  clients, slow log, monitor, pub/sub, command log, connection form,
  menus, dialogs, CSV/stream/group actions and Preferences. Dynamic application
  messages follow language changes; raw Redis values and server/OS diagnostics
  remain verbatim. Language files use ICU messages with named parameters and
  plural rules; adding a catalog automatically adds it to the language picker.
  See [Translations](I18N.md) for the contribution workflow.

## Server tools

Each connection has a page strip in the working card:

- **Status** reads the server's INFO sections: version, memory, clients,
  commands, CPU, persistence, replication, network rates and keyspace.
  Refresh is manual. A Sample button polls at the configured interval and adds
  clients, memory and network points to four mini history charts; the
  history is kept per session for the last 40 samples. A Clients button
  opens `CLIENT LIST` in a table with address, name, DB, idle time and
  current command.
- **CLI** runs arbitrary commands with quoted arguments and shows typed
  results. Up/Down recall input history and restore the current draft when
  navigating back to the end. `SELECT` switches the entire session and
  synchronizes the browser's DB selector. Streaming commands (MONITOR,
  SUBSCRIBE) have their own pages. Transactions and authentication/protocol
  changes remain unavailable in the pooled CLI.
- **Slow log** shows up to 64/128/256/512 entries with time, duration,
  client and command, filters by command/client, and can reset the
  server's slow log after a confirmation.
- **Monitor** streams every command the server runs over a dedicated
  raw RESP connection, so it does not take a pooled connection. It uses
  the profile's password, TLS and SSH tunnel. Start, stop, clear, copy,
  save, filter and follow-latest are in the page header/toolbar.
- **Pub/Sub** subscribes to channels or patterns, publishes messages,
  and shows messages with their channel, pattern and payload. Stop
  unsubscribes. Messages can be filtered, copied, saved and followed.
- **Command log** records the commands this app runs on the connection
  (the last 500), with duration and errors; it can be filtered, copied
  and saved.

Live monitor and pub/sub streams belong to the visible connection tab:
switching to another tab stops them. Switching to a tool page stops
pub/sub, and leaving Monitor stops the stream.

## Status

Working: connections and tabs, standalone/Sentinel/Cluster modes, TLS
with CA and client certificates, HTTP/SOCKS5 proxies, SSH password/key/
passphrase/agent tunnels, database switching where supported, paged key
browsing, basic key lifecycle, paged value browsing and filtering, row
editing for hash/list/set/zset, stream entries, RedisJSON editing, CSV
import/export, formats and decoders, the string editor, batch deletes/
TTLs, the Status, CLI, slow log, Monitor, Pub/Sub and command log tools,
preferences, keyboard shortcuts, the command palette, a GitHub release
check, connection groups/import/export, check mode, value copy/fullscreen/
auto refresh, full search, memory/length, binary key protection,
YAML/BitSet, ZStd/LZ4/Brotli/Msgpack/PHP/Pickle decoding, a custom decoder
command, CSV import options, status client list/network metrics,
slow-log filtering, monitor/pub-sub search/save/follow, pub/sub
publishing, command-log filtering, stream search/paging, status history
charts, stream consumer-group create/list/destroy plus consumer/pending
views, ACK and claim, named custom decoders, CSV export TTL control, and
English/Chinese UI with broad main-UI coverage, connection/tab keyboard
navigation, a pinned row editor, custom decoder encode support for
string/JSON and collection rows, automatic format detection, encoded editing,
exact/type filtering, configurable browsing, bulk collection additions,
connection options/ordering, CLI history/SELECT, stream consumer/pending operations with XAUTOCLAIM/XTRIM,
Ed25519-verified in-app updates, cross-platform packaging CI, and real
standalone/Sentinel/Cluster integration tests. Updates use a free signing key;
OS certificate signing is optional. See docs/RELEASING.md and PARITY.md for
the final desktop matrix; Web/Docker is excluded. Requires Redis 4 or
newer (for `UNLINK`).
