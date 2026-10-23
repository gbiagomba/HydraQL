# Changelog

All notable changes to **HydraQL** will be documented in this file.

## [2.7.0] - 2026-10-23
### Fixed
- **A partially built cluster is no longer thrown away.** CodeQL finalizes a `--db-cluster` as one step at the end, so a single language failing there leaves *every* language unfinalized and without its `db-*` directory, even languages whose extraction completed. HydraQL returned on the non-zero exit without finalizing anything and exited with "No databases were built successfully".

  A real run over a 26G monorepo requested five languages; JavaScript (183 TRAP files, 41M) and Python (206 TRAP files, 35M) had extracted completely and were discarded. Finalizing per language now recovers them: a copy of that JavaScript database finalizes cleanly and scans to 9 findings, while the C/C++ one still fails, which is the behavior this change relies on.

  HydraQL now finalizes each language regardless of the create exit code, classifies each requested language with `DBStructureOK`, reports which are ready and which extracted nothing, and fails a project only when no language is usable. `--allow-missing-db` is finally honored by the build phase, not just by detection.

### Added
- **Per-language build commands.** `--build-command` accepts an optional language prefix, so a tree that builds differently per language can be expressed in one run:
  ```bash
  --build-command java="./build.sh build-android" \
  --build-command swift="./build.sh build-ios"
  ```
  A value counts as per-language only when the text before the first `=` is a known language name with no whitespace, so a global command like `make CC=gcc` keeps its meaning. `kotlin=` resolves to java through `LangAlias`.

  Languages sharing a command list are built together in one `--db-cluster` invocation, exactly as before. When commands differ, each language is created on its own instead, because `--overwrite` against a cluster root destroys its sibling language directories: tested directly, a single-language cluster create with `--overwrite` left the sibling as an empty 4K directory that CodeQL then called "not a recognized CodeQL database". The per-language path costs one source scan per language, and the run says so when it takes that route.

### Changed
- `BuildDatabases` returns `[]BuildOutcome` with the per-language result for each project, rather than the list of projects that fully succeeded.

## [2.6.0] - 2026-10-23
### Added
- **`--external predicate=file.csv`**, forwarded to `codeql database analyze --external`. Repeat it for several predicates. A query declaring an extensional predicate cannot evaluate without one: `printAst.ql` fails with `The query depends on an extensional predicate selectedSourceFile which has not been defined`. The value is validated before any analysis starts, so a malformed pair, a missing file, or a directory given in place of a CSV is reported in a second rather than once per query. A leading `~` is expanded.

### Fixed
- **Graph formats wrote nothing.** `codeql database analyze --format=dot` treats `--output` as a *directory* and fills it with one file per graph, namespaced by rule id (`js/print-ast.dot`). HydraQL named a `.dot` file instead, so collecting the output failed with `read tmp_hydraql_output/printAst_proj_javascript.dot: is a directory` and no graph output was ever produced. Graph passes now name a directory, and the collection step copies whole trees so the rule namespacing survives and separate queries cannot collide.

  Verified end to end: `-o dot --external selectedSourceFile=<file>` now produces `HydraQL_output-<timestamp>-dot/printAst_proj_javascript/js/print-ast.dot`, byte-for-byte identical to a direct `codeql database analyze` invocation with the same arguments.
- Graph passes no longer attempt to count findings, since graph output describes a query's structure rather than alerts.
- `cleanTmp` removes the temp directory recursively; a graph pass leaves directories behind, not just files.

## [2.5.0] - 2026-10-23
### Changed
- **`-o all` is now a single CodeQL pass.** CodeQL runs once with `--format=sarif-latest`, and the CSV and JSON reports are reshaped from that SARIF instead of costing their own analysis. In 2.4.0 this took two passes.

  The CSV reconstruction is byte-for-byte identical to CodeQL's own writer. It was validated by running the same 95 queries over the same database both ways and diffing: 63 findings, no differences. Five conventions had to be reproduced to get there, each found by diffing rather than from documentation: `fullDescription` is used in preference to `properties.description` because CodeQL normalizes whitespace in the former and leaves multi-line `@description` text raw in the latter; paths are root-anchored with a leading `/`; SARIF's exclusive `endColumn` becomes inclusive; `startColumn` and `endLine` are restored from the SARIF defaults (1, and equal to `startLine`) when omitted; and message links are rewritten from SARIF's `[text](id)` into `[["text"|"relative:///path:line:col:line:col"]]` resolved through `relatedLocations`.

### Added
- **Raw CodeQL format passthrough.** Naming a CodeQL format sends that exact value to `codeql database analyze --format`:
  - `csv` runs CodeQL's own CSV writer in its own pass, so `-o csv` is unchanged from 2.4.0 and is not the reshaped output.
  - `sarif-latest` and `sarifv2.1.0` are forwarded as given and still merge, because HydraQL understands the SARIF 2.1.0 they produce. CodeQL records the spelling in `semmle.formatSpecifier`.
  - `dot`, `dgml`, and `graphtext` cannot be merged, since each describes one query's graph with no schema for combining several. Their per-query files are collected into `HydraQL_output-<timestamp>-<format>/` exactly as CodeQL wrote them.
- **Query `@kind` is now read** alongside the imported language, from the same single header read. Each pass only runs the queries its writer accepts: graph formats get `@kind graph` queries, and csv/json/sarif get `problem` and `path-problem` queries. Previously a graph query under a SARIF pass would simply fail and be logged.
- **Formats CodeQL no longer supports are rejected before any work starts**, naming the CLI version that removed them: `sarifv1` and `sarifv2` (removed in 2.8.2) and `lgtm` (removed in 2.23.5). CodeQL's own `--help` still advertises all three, and the CLI only rejects them once per query. A graph format requested with no `@kind graph` queries available is likewise refused up front rather than failing on every query.

## [2.4.0] - 2026-10-23
### Added
- **`--output-format` accepts several formats**, comma-separated, with `all` as a shorthand for `csv,json,sarif`. Each writes its own `HydraQL_output-<timestamp>.<ext>`, sharing one timestamp, and the run header reports how many CodeQL passes the request costs.

  `codeql database analyze --format` takes a single value, so the formats are grouped by the CodeQL pass that feeds them. JSON is reshaped from SARIF, so `json,sarif` costs one pass; CSV needs its own, so `all` costs two. The summary counts the first requested format, since every pass finds the same results.

  CSV is deliberately **not** derived from SARIF. Testing the two against each other over 68 findings turned up five classes of divergence: CodeQL's CSV writer normalizes whitespace in multi-line `@description` blocks, anchors paths with a leading `/`, converts SARIF's exclusive `endColumn` to an inclusive one, restores the `startColumn`/`endLine` values SARIF omits at their defaults, and rewrites message links from SARIF's `[text](index)` into its own `[["text"|"relative:///path:line:col:line:col"]]` form against `relatedLocations`. Reproducing all of that would be reimplementing CodeQL's CSV writer against undocumented conventions, so the second pass is the honest cost.

### Changed
- `RunQuery` takes the CodeQL format for the pass it is running, and `QueryResult` records it, so each report merges only the files written in its own format. `RunConfig.OutputFormat` is gone.

## [2.3.0] - 2026-10-23
### Added
- **`--build-command`**: the passthrough for `codeql database create --command`, needed by compiled languages (Java/Kotlin, C/C++, C#, Swift, Go). Repeat the flag for several commands; they are forwarded in order. It is a string array rather than a comma-split list, so a command containing a comma survives intact.
- **`--build-mode`**: passthrough for `codeql database create --build-mode`, accepting `none`, `autobuild`, or `manual`. Specifying a build command already implies manual mode, so the two are rarely needed together. An unknown mode, and the contradictory `--build-mode none` with a `--build-command`, are both rejected before any work starts.
- `BUILD_COMMAND` and `BUILD_MODE` Makefile variables.
- Both options also apply to the single-language `--auto-init-db` path, not just the cluster build.

### Fixed
- **`--source-dir .` produced a project named `project`.** The name was taken from the basename of the literal argument, which is empty for `.`, so it fell through to the generic fallback. Names are now derived from the resolved absolute path, making `--source-dir .` name the database after the directory it points at.

## [2.2.0] - 2026-10-23
### Added
- **One-stop build and scan** (`-b` / `--build-root`, `--source-dir`): HydraQL now creates the CodeQL databases it scans. For each project source directory it runs `codeql database create --db-cluster`, finalizes every language database in the cluster, then scans the result. This replaces the manual `codeql database create` and `codeql database finalize` shell loops.
- **Symlink resolution for source roots**, on by default. Every component of a source root is resolved with `filepath.EvalSymlinks` before CodeQL sees it. Pointing CodeQL at a directory of symlinks otherwise makes it follow them and extract their common ancestor: a tree of four symlinks into a monorepo produced a single database of 10,052,360 lines rooted at the monorepo, where resolving each link first produces four databases of 3k to 16k lines. Disable with `--no-resolve-symlinks`.
- **Multi-database scanning.** A run now fans out across every database under `--db-root` instead of one database per language. Three layouts are recognized: flat `<db-root>/<lang>/`, a single database at `<db-root>/`, and per-project `<db-root>/<project>/<lang>/`.
- **Project attribution in every output format**: a leading `Project` column in CSV, a `project` key per JSON record, and `automationDetails.id` plus `properties.hydraql.project` on each SARIF run so GitHub Code Scanning can tell runs apart.
- Per-project findings breakdown and a database count in the run summary.
- New flags: `--db-prefix`, `--exclude`, `--build-only`, `--rebuild-db`, `--no-resolve-symlinks`, `--build-timeout`, and `-B` / `--build-threads`.

### Fixed
- **`-o sarif` and `-o json` never worked.** Both passed their own name to `codeql database analyze --format`, which accepts neither: `Invalid value for option '--format': expected one of [CSV, csv, SARIFLATEST, sarif-latest, ...] but was 'sarif'`. Every query failed and every run reported zero findings. HydraQL now requests `sarif-latest` for both and derives the JSON records from the SARIF, adding flattened `ruleId`, `severity`, `messageText`, `path`, and `startLine` fields. An unknown `--output-format` is now rejected up front instead of silently falling through to SARIF. The same defect is present in the legacy Python implementation.
- **CSV merge dropped the first finding of every query.** `codeql database analyze --format=csv` emits bare data rows with no header, which is why the column names are hardcoded in this repo, but the merge and count paths both skipped row index 0. A header row is now detected by content rather than position.
- **Languages are deduplicated before database creation.** CodeQL resolves `typescript` to the `javascript` extractor and has no separate `typescript` language, so `--language=javascript,typescript --db-cluster` risked extracting the same files twice into two sibling directories.
- **`-X main.version` had no effect.** The Makefile injects the version at link time, but `version` was a `const`, which `-X` cannot patch. It is now a `var`.
- **`make run NO_PACK=1` passed `--no-pack-install`, a flag that exists in neither implementation**, so the run died on an unrecognized argument. Replaced with `PACK_INSTALL=1`, matching the opt-in `--pack-install` the CLI actually has.
- **Makefile paths containing spaces were split into separate arguments.** Path variables are now quoted at the use site, and the `LANGS` / `QUERY_DIRS` defaults no longer carry embedded quotes. The default `QUERY_DIRS` also no longer points at a nonexistent `~/Git/CQL`.
- Progress ticker output no longer races the next log line; stopping the ticker waits for the line to be cleared.
- `FinalizeDB` also treats "already been finalized" as success, matching the other phrasing CodeQL uses.
- **A multi-language cluster no longer needs `--allow-missing-db`.** `database create --db-cluster --language=javascript,python` against a JavaScript-only tree warns ("CodeQL detected code written in JavaScript/TypeScript ... but not any written in Python"), exits 0, and leaves an empty `python/` directory. Detection counted that as a finalize failure and aborted the run. A cluster language directory with no `db-*` subdir is now skipped quietly.
- **A missing Docker Hub credential failed the whole tag run.** The login step ran unconditionally on tags, so without `DOCKER_USERNAME` / `DOCKER_PASSWORD` it died on "Username and password required" and marked the run red even though the GitHub Release had published correctly. Docker Hub publishing is now detected and skipped with a notice annotation, and the image is still built and verified either way.
- **GitHub Releases had no release notes.** The CI step read the changelog under its old filename, matched `## [v2.2.0]` against headings written as `## [2.2.0]`, and used an awk range whose start line also matched its end pattern, so extraction always produced nothing and fell back to the raw tag message. All three are fixed.

### Changed
- `codeql database create --db-cluster` refuses to create its parent directory, so `--db-root` is created before the first build.
- **Makefile now drives the Go binary.** Added `make scan`, `make scan-sarif`, `make scan-json`, `make scan-high`, `make all` (build then scan), `make build-db` (build only), and `make go-fmt`. Previously the only run targets were the legacy Python ones. Build-phase variables (`BUILD_ROOT`, `SOURCE_DIRS`, `DB_PREFIX`, `EXCLUDE`, `REBUILD_DB`, `RESOLVE_SYMLINKS`, `BUILD_TIMEOUT`, `BUILD_THREADS`) and `FORCE_UNREADY` are exposed.
- `.dockerignore` excludes `cqlDB*/` so a local database tree is not uploaded into the Docker build context, and the Dockerfile documents the mounts each mode needs.
- `CHANGELOD.MD` renamed to `CHANGELOG.md`.
- Internal API: `codeql.DetectDatabases` is replaced by `codeql.DetectTargets` returning `[]ScanTarget`, `codeql.RunQuery` takes a `ScanTarget`, the `merge.Merge*` functions take `[]merge.Input`, and `report.PrintSummary` takes a `report.Summary`.

## [2.1.6] - 2026-07-06
### Fixed
- **Panic in `readLockPID`**: `FindStringSubmatch` returns `nil` when no match; slicing `nil[1:]` panicked with "slice bounds out of range [1:0]". Triggered whenever a `.lock` file exists with empty or non-standard content. Guard added: only slice when `len(m) > 1`.

## [2.1.5] - 2026-07-06
### Fixed
- **`go install` now works**: Updated module path from `github.com/gbiagomba/hydraql` to `github.com/gbiagomba/hydraql/v2` per Go major-version convention. Without `/v2`, the Go toolchain ignored all v2.x.x tags and resolved `@latest` to v1.8.0 (Python, no `cmd/hydraql`).
  ```
  go install github.com/gbiagomba/hydraql/v2/cmd/hydraql@latest
  ```

## [2.1.4] - 2026-07-06
### Changed
- **Merged Docker CI jobs**: `Build Docker Image` + `Publish Docker Image` consolidated into a single `Docker` job. Branch pushes build only (no push); tag pushes build and push to Docker Hub.
- **Updated all GitHub Actions** to latest major versions: `actions/checkout` v4→v7, `actions/setup-go` v5→v6, `docker/setup-qemu-action` v3→v4, `docker/setup-buildx-action` v3→v4, `docker/login-action` v3→v4, `docker/build-push-action` v6→v7. Resolves Node.js 20 deprecation warnings.

## [2.1.3] - 2026-07-06
### Fixed
- **CI run backlog**: Added `concurrency` group to workflow — new branch pushes cancel any in-progress run for the same ref. Tag runs (`v*`) are never cancelled.
- **Stale tag runs**: `v2.1.1` and `v2.1.2` tags pointed to commits still containing `macos-13`; this tag is the first on a clean commit without it.

## [2.1.2] - 2026-07-06
### Fixed
- **Windows CI gofmt failure**: Windows git checks out files with CRLF by default; `gofmt` expects LF and flagged every `.go` file as unformatted. Added `.gitattributes` forcing `eol=lf` for `*.go`, `*.yml`, `*.sh`, and `Makefile`.

## [2.1.1] - 2026-07-06
### Fixed
- **Docker build 404**: `codeql-linux-arm64.zip` does not exist in any CodeQL CLI release. Removed the arch-aware switch; always download `codeql-linux64.zip`. Docker image is `linux/amd64` only (CodeQL has no Linux arm64 binary).
- **Docker arm64 platform removed** from `docker-build` and `publish-docker` CI jobs for the same reason.
### Changed
- **Go version**: `golang:1.21` → `golang:1.26-bookworm` in Dockerfile; `GO_VERSION` in workflow updated to `1.26`; `go.mod` updated to `go 1.26`.
- **CodeQL CLI version**: default `CODEQL_VERSION` updated `2.17.6` → `2.25.6` (latest).

## [2.1.0] - 2026-07-06
### Added
- **Short flags** via `pflag` (github.com/spf13/pflag): every flag now has a short single-character alias (`-d`/`--db-root`, `-l`/`--langs`, `-q`/`--query-dir`, `-F`/`--auto-finalize-db`, `-m`/`--allow-missing-db`, `-u`/`--unlock-cache`, `-c`/`--check-lock-process`, `-f`/`--fancy`, `-v`/`--verbose`, etc.). All existing long flags unchanged — fully backward compatible.
- **`--parallel` preserved** as a hidden alias for `--threads` to match existing command usage.
- **GitHub Actions CI/release workflow** (`.github/workflows/ci-release.yml`): build-test matrix (ubuntu/macos-13/macos-14/windows), cross-compile release job (`make build-all` → 6 binaries + sha256 checksums → GitHub Release), multi-arch Docker build/publish.
- **Go multi-stage Dockerfile**: `golang:1.21` builder + `debian:bookworm-slim` final; arch-aware CodeQL CLI install (amd64 `linux64`, arm64 `linux-arm64`); no Python runtime in image.
- **Updated `.dockerignore`**: excludes `dist/`, `legacy/`, build artifacts.

## [2.0.0] - 2026-07-06
### Added
- **Go rewrite**: HydraQL is now a single self-contained Go binary with no runtime dependencies.
  - `cmd/hydraql/main.go` — flag parsing, worker pool, orchestration
  - `internal/codeql/` — DB detection, query discovery, per-DB semaphore, per-query context timeout, progress ticker
  - `internal/merge/` — CSV/JSON/SARIF merging and severity filtering
  - `internal/report/` — ANSI color constants, ASCII chart
- **Cross-compiled binaries** via `make go-build-all` (6 targets: darwin/linux/windows × amd64/arm64)
- **`dist/` directory** — pre-built binaries land here; `make go-build` for native, `make go-build-all` for all platforms
- All v1.8.0 Python features are present: `--query-timeout`, `--no-timeout`, progress ticker, timed-out count in summary

### Changed
- Python implementation moved to `legacy/hydraql.py` (still functional, `make run` uses it)
- Install scripts (`install.sh`, `install.ps1`) moved to `legacy/`
- Makefile split into Go targets (`go-build`, `go-build-all`, `go-test`, `go-vet`, `go-clean`) and Python legacy targets

### Notes
- Go binary is the recommended production path going forward
- Python legacy version remains at v1.8.0 feature level

## [1.8.0] - 2026-07-06
### Added
- **Per-query timeout** (`--query-timeout <seconds>`, default 600s): CodeQL queries that exceed the limit are killed, logged to `hydraql_failures.log` as `TIMEOUT after Xs: <query> on <lang>`, and skipped — the scan continues without them.
- **`--no-timeout` flag**: Disables the per-query timeout entirely for cases where long runtimes are expected (e.g., large suites on massive codebases).
- **Real-time progress ticker**: A background thread prints elapsed time every 5 seconds (`⏳ [lang] query.ql … Xs elapsed`) to stderr during query execution, making it easy to distinguish a slow query from a hung one.
- **Timed-out query count in summary**: The final summary now reports how many queries timed out, with a hint to increase `--query-timeout` or use `--no-timeout`.

## [1.7.2] - 2025-09-04
### Added
- Per-DB concurrency control: HydraQL runs queries **sequentially per DB** to avoid IMB cache contention while still running different DBs in parallel.
- Finalize/lock smart retry: if a query fails with “needs to be finalized” or IMB lock contention, HydraQL will auto-fix (finalize/unlock) and **retry once**.

### Changed
- Lock handling now runs **right before each query** (in addition to discovery), ensuring stale locks created between discovery and analyze are removed.
- With `--auto-finalize-db`, HydraQL now runs `codeql database finalize <db>` **unconditionally** for each DB (idempotent), rather than relying on metadata heuristics.

### Fixed
- Some finalized-but-empty/“no source code seen” DBs caused analyze attempts. These are skipped by default unless `--force-scan-unready` is provided.

## [1.7.1] - 2025-09-04
### Added
- `--pack-install` (opt-in) to run `codeql pack install` before scanning.
- Aggressive, **recursive** `.lock` deletion with chmod/truncate/rename fallbacks.
- Re-check locks after `--auto-finalize-db`.

### Changed
- Pack install is **off by default** (previously on by default).

## [1.7.0] - 2025-08-30
### Added
- IMB cache lock handling flags:
  - `--unlock-cache` to delete stale `.lock` files.
  - `--check-lock-process` to read/inspect PID in lock file.
  - `--kill-lock-process` to `kill -9` the PID found in the lock (use with caution).
- DB readiness automation:
  - `--auto-finalize-db` to automatically finalize unfinalized DBs.
  - `--auto-init-db` + `--source-root <path>` to create missing DBs.
- Empty/unusable DB detection (finalized but “no source code seen” cases):
  - Skip such DBs **by default** with a new heuristic.
  - Override with `--force-scan-unready`.
- `--suite-only` to run only `.qls` suites.
- Auto **suite preference**: if `.qls` exists in a query directory, prefer suites there.
- `--verbose` for detailed command logs and error tails.

### Changed
- `codeql pack install` now runs **by default** (disable with `--no-pack-install`).
- Severity filtering supports **strict** (`--strict-severity`) vs **loose** (default mapping `error→CRITICAL`, `warning→HIGH`, `note→MEDIUM`, plus substring).
- Per-query finding counts for **CSV, JSON, SARIF**.
- CSV merge always writes the fixed header:  
  `Name,Description,Severity,Message,Path,Start line,Start column,End line,End column`.

### Fixed
- Parallel runs now surface failures in `hydraql_failures.log` and summarize them.
- Query discovery robust to repeated `--query-dir` and comma-separated inputs.
- Typescript/JavaScript DB aliasing honored (`typescript → javascript`).

## [1.6.0] - 2025-08-29
### Added
- ASCII summary chart and fancy colored output.
- Severity filtering across CSV/JSON/SARIF merges.

### Fixed
- Guaranteed CSV headers even when there are no findings.

## [1.5.0] - 2025-08-28
### Added
- Initial Python port “HydraQL” with parallel query execution.
- Multi query dirs, language aliasing, DB preflight checks (presence/finalized), pack install.
- Output formats: CSV, JSON, SARIF.

## [1.2.0] - 2025-08-28
### Added
- Added `--allow-missing-db` flag to continue processing even if some databases are missing or unfinalized
- Improved performance of database detection and query execution
- Updated documentation with new examples and usage instructions

## [1.1.1] - 2025-08-27
### Added
- Added support for additional languages: Ruby, Swift
- Improved error handling for database detection
- Enhanced output formatting options
