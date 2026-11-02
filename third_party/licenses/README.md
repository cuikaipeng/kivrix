# License texts

These files are the upstream license texts for Kivrix's dependencies,
icons and adapted code. See [`../../THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md)
for attribution and the full component list.

- `kero.txt` and `mygo.txt`: MIT notices for adapted UI code and the framework.
- `lucide.txt`: ISC and Feather MIT notices for icons.
- `go-redis.txt`, `klauspost-compress.txt`, `lz4.txt`, `msgpack.txt`,
  `gopickle.txt`, `tagparser.txt`, `x-crypto.txt`, `x-image.txt`,
  `x-sys.txt`, `x-text.txt`, `xxhash.txt`: BSD notices.
- `purego.txt`: Apache-2.0.
- `brotli.txt`, `miniredis.txt`, `phpserialize.txt`, `gopher-lua.txt`,
  `uber-atomic.txt`: MIT.
- `yaml.txt`: the project's MIT and Apache-2.0 licenses for different files;
  its additional `NOTICE` is retained in the module copy.
- `go-text-typesetting.txt`: Unlicense OR BSD-3-Clause.

`modules/` includes each module's nested license and NOTICE files. The short
named copies above retain the original top-level texts. `go-redis-components.txt`
also preserves the client's file-level Apache, ISC and CRC16 notices.
`MODULES.md` records the complete selected module graph and the one unused
module whose selected revision has no license. It must not enter a build.

Check with `python3 scripts/licenses.py`. After reviewing new upstream terms,
refresh with `python3 scripts/licenses.py --write`; this also synchronizes
`resources/licenses/`. The inventory is an integrity check, not legal approval.
