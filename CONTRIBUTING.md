# Contributing

Thanks for helping Kivrix. The project is desktop-only and aims to stay
native, small and predictable.

## License of contributions

Kivrix is MIT licensed. By opening a pull request you agree that your
contribution is licensed under the same MIT terms and that you have the right
to submit it. Sign off commits with `git commit -s` to certify the
[Developer Certificate of Origin 1.1](https://developercertificate.org/).

## Third-party and GPL material

- Tiny RDM is GPL-3.0 and is used only as a feature/UX reference. **Do not
  copy Tiny RDM source code, translations, themes, images or other assets**
  into this repository unless the project is deliberately relicensed under a
  compatible GPL.
- kero is MIT licensed; adapted code must keep its copyright and license
  notice. See `third_party/licenses/kero.txt`.
- New dependencies must have a license compatible with MIT distribution.
  Add their copyright/license text to `third_party/licenses/` and update
  `THIRD_PARTY_NOTICES.md`.
- Review the exact dependency version and file-level licenses, including
  `NOTICE`, nested libraries, fonts and assets. License names at the repository
  root alone are not sufficient. Record the source and license of copied or
  adapted code in the relevant file and third-party notices. A rewrite of
  identifiers or a translation to another language does not erase authorship.
- After that review, run `python3 scripts/licenses.py --write` and inspect the
  generated diff. CI runs `python3 scripts/licenses.py` to reject dependency
  drift, missing notices or stale packaged copies. It does not decide whether
  a new license is acceptable. See `docs/OPEN_SOURCE_AUDIT.md`.
- Do not remove existing notices from `LICENSE`, `THIRD_PARTY_NOTICES.md` or
  `resources/licenses/`.

## Development

```sh
go tool mygo dev
make test
make race
make vet
```

## Testing

The default suite runs without a desktop or an installed Redis. It covers the
store, codecs, connection security, resource limits, translations and UI
components. UI flow tests also drive real views and background workers through
an isolated miniredis server: connecting/auth retry, key CRUD, CLI/database
switching, collection paging, CSV transfer, encoded-save failures/retries and
Pub/Sub cleanup. They process queued UI callbacks on one test thread, like
`Window.Update`, so they verify completion/error state as well as server data.
Controlled callback delivery covers stale reads, edits made during a save and
connections finishing after the application closes.
Update flow tests cover startup and manual checks, bounded progress delivery,
download retries, close-time cancellation, and explicit restart with unsaved
edits. Release CI also verifies the signatures of the actual update archives.

Useful focused commands:

```sh
make e2e          # headless UI flows and asynchronous regression tests
make coverage    # build/coverage.out; inspect with go tool cover -html=build/coverage.out
make integration # disposable real Redis: standalone, Sentinel, Cluster, Unix socket
make test-native # opens a real window; requires a desktop session and native build tools
python3 -m unittest discover -s scripts -p 'test_*.py'
```

Real Redis tests cover protocol behavior that miniredis cannot reproduce,
including a UI scan/filter/edit/status/MONITOR flow. `scripts/integration.sh`
requires `redis-server`, `redis-cli` and Python 3, checks its client and cluster-bus
ports, binds only to loopback, and cleans up its temporary instances. Readiness
checks verify each server's process ID. Use it rather than pointing tests at a
production server.

The opt-in native smoke test calls the production application entry point with
temporary preferences, profiles and vault storage. It checks session restoration,
window rendering, the native update menu, and connection cleanup on close.
It uses no personal Redis connections, disables startup checks, and exercises
the manual check in a development build without network access. It is verified locally on macOS; the
headless flows do not exercise OS mouse/keyboard delivery. File dialogs,
clipboard permissions, IME/accessibility behavior and installer first launch
still need platform testing. This suite is not a guarantee against every bug or
a replacement for CPU/memory profiling under large workloads.

`make` saves failed UI-flow screenshots and the native screenshot under
`build/test-artifacts/`; override `KIVRIX_TEST_ARTIFACTS` to change the directory.
Do not use golden-image comparisons for platform-dependent font rendering.
CI runs coverage, real Redis and race checks in one Linux job on PRs and main;
release/manual packaging also runs the native smoke test on its existing macOS
runner. Coverage reports and screenshots are retained for seven days. Add
regression scenarios for observable behavior, rather than asserting internal
implementation details or targeting 100% coverage.

Keep MyGo UI lifetimes in mind: create elements in the current view build,
publish asynchronous results with `App.update`, and use `ui.Tester` with a
fresh `App`/`Tester` pair where needed. See `docs/RELEASING.md` for packaging.

For adding a language or changing UI messages, see [Translations](docs/I18N.md).
