# Changelog

All notable changes to **HydraQL** will be documented in this file.

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
