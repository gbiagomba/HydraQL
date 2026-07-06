# Changelog

All notable changes to **HydraQL** will be documented in this file.

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
