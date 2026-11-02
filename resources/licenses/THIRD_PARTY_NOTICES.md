# Third-party notices

Kivrix-authored code is distributed under the MIT License (see `LICENSE`).
Third-party code and assets retain their own licenses; the root MIT license
does not relicense them. This
file records the third-party software, visual references and assets used
by the project, and where their license texts are kept. Full texts are in
[`third_party/licenses/`](https://github.com/cuikaipeng/kivrix/tree/main/third_party/licenses). They are also copied
into the packaged app's resources.

## Framework and adapted code

| Component | License | Use |
|---|---|---|
| [MyGo](https://github.com/egoist/mygo) 0.6.2 | MIT | Native Go UI framework, windowing and app packaging; framework menu/control translations in `internal/app/locales/*.json`. |
| [kero](https://github.com/egoist/kero) | MIT, Copyright (c) 2026 EGOIST | Visual language and some layout/control code adapted: the settings window layout, segmented/pop-up/switch styling, sidebar treatment, background washes and palette structure. The MIT notice is retained in `third_party/licenses/kero.txt`. |
| [Lucide](https://lucide.dev/) / Feather | ISC and MIT for Feather-derived icons | Interface icons. Both notices, including Cole Bemis's copyright, are in `third_party/licenses/lucide.txt`. |

## Design/feature references — no code copied

| Project | License | Relationship |
|---|---|---|
| [Tiny RDM](https://github.com/tiny-craft/tiny-rdm) | GPL-3.0 | Used only as a product/feature and UX reference. **No Tiny RDM source code, translations, themes, images or other assets are included in this repository.** Kivrix is an independent implementation. If you contribute code copied from Tiny RDM or another GPL project, that code cannot be accepted under this project's MIT license. |
| [Redis Insight](https://github.com/redis/RedisInsight) | SSPL-1.0 in the upstream repository checked on 2026-10-30 | General product inspiration only. No code or assets used. Its source-available license must not be treated as permission to relicense copied code as MIT. |

The app icon in `resources/icon.png` is AI-generated project artwork created
for Kivrix during this project's design work. `docs/images/logo.svg` embeds
the same artwork with rounded corners for the README. `docs/images/kivrix.png` is a
screenshot of Kivrix. Project-owned rights in these assets are provided under
the root MIT license; depicted third-party icons retain their notices above.
No Tiny RDM or kero artwork is included. Generated artwork does not establish
exclusive copyright or trademark clearance.

Redis and RedisJSON are trademarks of Redis Ltd. Kivrix is not
affiliated with or endorsed by Redis Ltd., Tiny RDM, kero, or the Go
project.

## Go module dependencies

The following modules are linked into the application or used by its
tests. License texts are copied under `third_party/licenses/`.

| Module | License | Used for |
|---|---|---|
| Go runtime and standard library | BSD-3-Clause | Compiled into the app (`third_party/licenses/go.txt`) |
| github.com/egoist/mygo | MIT | Native UI and app runtime |
| github.com/redis/go-redis/v9 | BSD-2-Clause, with BSD-3-Clause / ISC / Apache-2.0 portions | Redis client; file-level notices in `go-redis-components.txt` |
| github.com/ebitengine/purego | Apache-2.0 | MyGo's cgo-free platform bindings |
| github.com/klauspost/compress | BSD-3-Clause; additional MIT/Apache-2.0 component notices | Zstandard |
| github.com/pierrec/lz4/v4 | BSD-3-Clause | LZ4 |
| github.com/andybalholm/brotli | MIT | Brotli |
| github.com/vmihailenco/msgpack/v5 | BSD-2-Clause | MessagePack |
| github.com/elliotchance/phpserialize | MIT | PHP serialization |
| github.com/nlpodyssey/gopickle | BSD-2-Clause | Python pickle |
| gopkg.in/yaml.v3 | MIT AND Apache-2.0 (different files) | YAML formatting and parsing; preserve both licenses and `NOTICE` |
| golang.org/x/crypto | BSD-3-Clause | SSH |
| github.com/alicebob/miniredis/v2 | MIT | Tests only |
| github.com/cespare/xxhash/v2 | BSD-3-Clause | go-redis dependency |
| github.com/go-text/typesetting | Unlicense OR BSD-3-Clause; MIT in its HarfBuzz subdirectory | MyGo Unicode text helpers; HarfBuzz is not linked in the current app |
| github.com/vmihailenco/tagparser/v2 | BSD-2-Clause | msgpack dependency |
| github.com/yuin/gopher-lua | MIT | Miniredis test dependency |
| go.uber.org/atomic | MIT | go-redis dependency |
| golang.org/x/image | BSD-3-Clause | MyGo text/images |
| golang.org/x/sys | BSD-3-Clause | Platform bindings |
| golang.org/x/text | BSD-3-Clause | Text |

The selected versions come from `go list -m all`; `go.sum` verifies downloaded
modules. The generated inventory in
[`third_party/licenses/MODULES.md`](https://github.com/cuikaipeng/kivrix/blob/main/third_party/licenses/MODULES.md) records
all selected modules, including upstream test/tool/optional dependencies
that are not linked into Kivrix. Its module copies preserve nested license
files and `NOTICE` files as well as top-level licenses. They include, for
example, the Snappy-Go and Zstandard xxhash notices, YAML's `NOTICE`, MyGo's
shader compiler notice and the separate notices inside miniredis.

One old graph-only module, `github.com/chzyer/logex@v1.1.10`, contains no
license in that revision. It is not used by Kivrix, Kivrix's tests or the MyGo
build tool. No logex source or binary is distributed. The license check blocks
it from entering any of those builds; its presence in the upstream module
graph is not a grant of permission to use it.

Run `python3 scripts/licenses.py` to check selected versions, notice integrity,
all five desktop package closures and packaged copies. After reviewing a
dependency change, `python3 scripts/licenses.py --write` refreshes the copies.
This check detects omissions and drift, not copyright ownership or legal
compatibility by itself. See [the audit record](https://github.com/cuikaipeng/kivrix/blob/main/docs/OPEN_SOURCE_AUDIT.md).

If you redistribute a built binary, keep `LICENSE`, this file and the
`third_party/licenses` directory with it. The `resources/licenses` directory in the app bundle
is provided for that purpose.
