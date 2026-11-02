# Open-source review — 2026-10-30

Kivrix can retain MIT for its own code on the evidence reviewed here. Its
dependencies retain their original terms. This is a source and packaging
compliance review, not a legal opinion, an ownership warranty, or a guarantee
against copyright, patent or trademark claims.

## Scope and evidence

- Reviewed the current working tree, `LICENSE`, direct and transitive module
  notices, inline source notices, SVG icons, the app icon, screenshot, build
  scripts and distribution resources. No source commits or publication were
  made. There was no committed project history available to establish a full
  provenance chain.
- `go list -m all` selects 42 external modules. Package dependency checks for
  macOS amd64/arm64, Linux amd64/arm64 and Windows amd64 find 18 external
  runtime modules. Project tests add miniredis and gopher-lua; the MyGo build
  tool uses MyGo and x/text, already included. The remaining graph entries
  are upstream optional/tool/test dependencies, not shipped application code.
- Runtime licensing found in that scope is permissive: MIT, BSD, ISC,
  Apache-2.0 and the Unlicense/BSD alternative. Some modules have different
  licenses for individual files. No GPL, AGPL or SSPL dependency was found
  in the application's package closures. Kivrix connects to Redis over its
  protocol; it does not bundle the Redis server or Redis Insight.
- The complete selected module inventory, upstream notice filenames and
  integrity hashes are in `third_party/licenses/inventory.json` and
  `MODULES.md`. Nested notices can describe subcomponents not used by Kivrix;
  their inclusion is not evidence that those subcomponents are shipped.

## Reference projects and assets

| Source | Evidence and result |
|---|---|
| [Kero at 4da2172](https://github.com/egoist/kero/tree/4da21721c6ecb3936161c2ec0a59bed755073bf1) | MIT, Copyright (c) 2026 EGOIST. Settings controls contain an adapted matching block; palette/window/sidebar work is also attributed. The full upstream license matches the retained copy. Added file-level attribution in `app.go`, `prefsview.go`, `theme.go`, `shell.go` and `run.go`. Its icons, fonts and logos were not imported. |
| [Tiny RDM at 4ddb94a](https://github.com/tiny-craft/tiny-rdm/tree/4ddb94a3c55f3401dbc66870f0d21cf0d6203cbc) | GPL-3.0. Compared its 66 Go source files with Kivrix production Go code and compared the English/Chinese catalogs. No normalized matching block of six substantive lines totaling at least 150 characters was found. Matching catalog values were short interface terms, not long messages. This supports the recorded independent implementation; it does not prove absence of adapted code. |
| [MyGo v0.6.2](https://github.com/egoist/mygo/tree/v0.6.2) | MIT core framework and copied framework menu translations. Additional subcomponent notices, including its Apache-2.0 shader compiler, are retained in the module copy. |
| [Lucide / Feather](https://github.com/lucide-icons/lucide/blob/main/LICENSE) | ISC plus MIT for Feather-derived icons. The retained full license matches upstream and includes Cole Bemis's notice. Corrected summaries that mentioned only ISC. |
| [Redis Insight license](https://github.com/redis/RedisInsight/blob/main/LICENSE) | The current repository declares SSPL-1.0, not simply “proprietary.” It is a product reference, not an imported dependency. No Redis Insight source or assets were identified in Kivrix. |
| `resources/icon.png` | Generated for this project during the recorded design conversation; not a downloaded reference-project logo. Corrected the obsolete “Redis Pilot” attribution. AI generation does not prove exclusivity or trademark clearance. |
| `docs/images/kivrix.png` | A screenshot of Kivrix, visually inspected; uses the application's own layout and attributed interface icons. |

Copyright distinguishes ideas and operation methods from their protected
expression; this does not authorize copying another project's code, artwork,
text or distinctive expression. See the
[U.S. Copyright Office explanation](https://www.copyright.gov/help/faq/faq-protect.html)
for that distinction; applicable law varies by jurisdiction. Changing variable
names or translating a program to another language is not a substitute for
permission. Kero's MIT permission requires retaining its copyright and license.

## Issues corrected

1. **Incomplete dependency notices.** Regenerated module copies from the exact
   selected versions, retaining nested `LICENSE`, `COPYING` and `NOTICE` files.
   Added file-level go-redis notices for Camlistore's Apache-2.0 code, Harald
   Rudell's ISC code, and the CRC16 implementation. Nested notices also retain
   Snappy-Go and the MIT-licensed Zstandard xxhash implementation.
2. **YAML licensing misstated.** Its [LICENSE](https://github.com/go-yaml/yaml/blob/v3.0.1/LICENSE)
   applies MIT to the libyaml-derived files and Apache-2.0 to other files.
   Both apply; this is not a choice of either license. Its separate `NOTICE`
   is now retained. Apache's redistribution conditions include carrying
   applicable notices: [Apache-2.0 §4](https://www.apache.org/licenses/LICENSE-2.0).
3. **Unlicensed but unused graph entry.**
   [`chzyer/logex` v1.1.10](https://github.com/chzyer/logex/tree/cd112f618178aaaf4ea8592c8839f5276145d9cf)
   has no license in that revision. `go mod why -m` says the main module does
   not need it; it enters the module graph through gopher-lua. It is absent
   from the app, project-test and build-tool package closures on all five
   targets. No source or binary from it is redistributed. Checks explicitly
   reject it becoming used; do not infer permission from its public repository.
4. **Stale/inaccurate attribution.** Corrected Lucide/Feather, Redis Insight,
   YAML and the generated logo statements, and clarified that Kivrix's MIT
   license does not replace dependency licenses.
5. **Packaging only checked for a top-level license.** Packaging now compares
   every bundled notice with the reviewed set, including DMG app resources,
   Windows staging, Linux tar/DEB packages and in-app update archives. CI also
   rejects dependency-version drift, changed/missing notice copies and stale
   `resources/licenses` files. Windows installer execution still requires
   Windows validation; its packaging uses MyGo's recursive resource inclusion.

## Maintenance

```sh
# Read-only check, also run by CI and before packaging.
python3 scripts/licenses.py

# After manually reviewing new versions, terms and provenance:
python3 scripts/licenses.py --write
python3 -m unittest discover -s scripts -p 'test_*.py'
```

The generator copies notices; it does not decide license compatibility.
Review the generated diff and source-level licenses when upgrading a
dependency. Record source revisions and preserve notices for adaptations.
Do not import Tiny RDM GPL code or Redis Insight SSPL code into the MIT
implementation without a compatible licensing plan or separate permission.

## Remaining limits

Similarity checks cannot reconstruct all development history or detect every
rewrite, copied design or unrecorded input. Statements of independent origin
remain dependent on contributor provenance. Existing DCO sign-off guidance
helps record future contributions but is not proof of ownership.

This review is not a jurisdiction-specific trademark or patent clearance
search. The coined name and generated logo are not guaranteed exclusive.
No unknown claim can be ruled out by adding license files. Before asserting
an unconditional legal guarantee or making a significant commercial launch,
obtain qualified legal review of provenance and relevant jurisdictions.
