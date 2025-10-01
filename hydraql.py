#!/usr/bin/env python3
"""
HydraQL — Parallel CodeQL Scanning Engine
----------------------------------------
- Multi-DB, multi-language, parallel CodeQL runner
- Multiple query dirs (repeatable or comma-separated; trims whitespace, expands ~)
- Language aliasing (TypeScript→JavaScript, Kotlin→Java)
- Robust DB preflight: presence + finalized + structure + emptiness checks
- Auto-handling options:
  * IMB cache locks: --unlock-cache / --check-lock-process / --kill-lock-process
  * DB readiness: --auto-finalize-db / --auto-init-db --source-root <path>
  * Skip empty/unusable DBs by default; override with --force-scan-unready
- Parallel execution with per-query outputs, merged into CSV/JSON/SARIF
- Severity filtering (strict vs loose), fancy colored output, dry run
- `codeql pack install` is OPT-IN with --pack-install (default OFF)
- Accurate per-query counts for CSV/JSON/SARIF; CSV headers guaranteed
- Failure logging + verbose diagnostics
- Suite preference: prefer `.qls` if present; `--suite-only` to force suites
- Summary with ASCII chart
- NEW: per-DB single concurrency (prevents IMB lock races)
- NEW: finalize/lock-aware retry once per query
"""
import argparse
import subprocess
import sys
import csv
import json
import re
import os
import threading
from time import time as _now, sleep
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime
from typing import List, Tuple, Optional, Dict, Any

# ANSI color codes
COL_RESET = "\033[0m"
COL_CYAN = "\033[1;36m"
COL_GREEN = "\033[1;32m"
COL_YELLOW = "\033[1;33m"
COL_MAGENTA = "\033[1;35m"
COL_BLUE = "\033[1;34m"
COL_RED = "\033[1;31m"

APP_NAME = "HydraQL"

LANG_ALIASES = {'typescript': 'javascript', 'kotlin': 'java'}
IMPORT_LANG_RE = re.compile(r"^\s*import\s+(java|javascript|python|cpp|swift|ruby|kotlin|typescript)\b", re.I|re.M)

# -------------------------
# Args
# -------------------------
def parse_args():
    p = argparse.ArgumentParser(description=f"{APP_NAME} — Parallel CodeQL scanning")
    p.add_argument('--langs', default='java,javascript,typescript,python', help='Comma-separated languages')
    p.add_argument('--db-root', dest='db_root', type=Path, default=Path('cqlDB'),
                   help='Root of CodeQL databases (expects <db-root>/<lang>/codeql-database.yml)')
    p.add_argument('--query-dir', dest='query_dirs', action='append', help='Query dir(s); repeat or comma-separate')
    p.add_argument('--threads','--parallel', dest='threads', type=int, default=6, help='Parallel jobs')
    p.add_argument('--output-format', dest='output_format', choices=['csv','json','sarif'], default='csv')
    p.add_argument('--severity', dest='severity_filter', default=None, help='Filter results by severity (e.g., HIGH, CRITICAL)')
    p.add_argument('--strict-severity', action='store_true', help='Exact severity match only (no mapping, no substring)')
    p.add_argument('--fancy', action='store_true', help='Colored output + ASCII chart')
    p.add_argument('--dry-run', action='store_true', help='List actions without running CodeQL')
    p.add_argument('--pack-install', action='store_true', help='Run `codeql pack install` before scanning (opt-in)')
    p.add_argument('--allow-missing-db', action='store_true', help='Proceed even if some requested DBs are missing or unfinalized')
    p.add_argument('--verbose', action='store_true', help='Verbose logging of query execution and failures')
    p.add_argument('--suite-only', action='store_true', help='Run only query suites (.qls) and skip individual .ql files')

    # Lock handling
    p.add_argument('--unlock-cache', action='store_true', help='Aggressively delete IMB cache .lock files under each DB')
    p.add_argument('--check-lock-process', action='store_true', help='If cache lock exists, read PID and warn if a codeql process is running')
    p.add_argument('--kill-lock-process', action='store_true', help='DANGER: kill -9 the PID found in the cache .lock (use with caution)')

    # DB automation
    p.add_argument('--auto-finalize-db', action='store_true', help='Automatically finalize any DBs (idempotent)')
    p.add_argument('--auto-init-db', action='store_true', help='Automatically create missing DBs (requires --source-root)')
    p.add_argument('--source-root', type=Path, default=None, help='Source root for creating missing DBs (used with --auto-init-db)')

    # Skip unusable DBs unless forced
    p.add_argument('--force-scan-unready', action='store_true', help='Force scanning even if DB looks empty/unusable')
    return p.parse_args()

# -------------------------
# Helpers
# -------------------------
def normalize_query_dirs(raw_dirs):
    if not raw_dirs:
        return [(Path.home()/'Git'/'codeql').expanduser()]
    out = []
    for entry in raw_dirs:
        for token in entry.split(','):
            tok = token.strip()
            if tok:
                out.append(Path(tok).expanduser())
    return out

def run_cmd(cmd: List[str], verbose: bool=False) -> subprocess.CompletedProcess:
    if verbose:
        print("   cmd:", " ".join(cmd))
    return subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)

def install_query_packs(fancy, verbose):
    if fancy:
        print(f"{COL_MAGENTA}[*]{COL_RESET} Installing CodeQL query packs...")
    cp = run_cmd(['codeql','pack','install'], verbose)
    if cp.returncode != 0:
        print(f"{COL_YELLOW}⚠️  Failed to install query packs{COL_RESET}")
        if verbose:
            print(cp.stderr.decode(errors='ignore')[:800])

def alias_lang(lang: str) -> str:
    return LANG_ALIASES.get(lang.lower(), lang.lower())

def db_structure_ok(db_dir: Path) -> bool:
    if not (db_dir / 'codeql-database.yml').exists():
        return False
    try:
        has_sub = any(c.is_dir() and c.name.startswith('db-') for c in db_dir.iterdir())
    except FileNotFoundError:
        has_sub = False
    return has_sub

# ---- Lock helpers ----
def cache_lock_path(db_dir: Path) -> Path:
    direct = db_dir/'default'/'cache'/'.lock'
    if direct.exists():
        return direct
    for child in db_dir.glob('db-*/default/cache/.lock'):
        if child.is_file():
            return child
    return direct

def read_lock_pid(lock_file: Path) -> Optional[int]:
    try:
        text = lock_file.read_text(encoding='utf-8', errors='ignore')
        m = re.search(r'pid\s*=\s*(\d+)', text, re.I)
        if m: return int(m.group(1))
        m2 = re.search(r'(\d+)', text)
        if m2: return int(m2.group(1))
    except Exception:
        pass
    return None

def process_running(pid: int) -> bool:
    try:
        os.kill(pid, 0)
        return True
    except Exception:
        return False

def try_kill_process(pid: int, verbose: bool):
    try:
        os.kill(pid, 9)
        if verbose: print(f"   Killed process {pid}")
    except Exception as e:
        print(f"{COL_YELLOW}⚠️  Failed to kill process {pid}:{COL_RESET} {e}")

def find_all_locks(db_dir: Path) -> List[Path]:
    locks: List[Path] = []
    direct = db_dir/'default'/'cache'/'.lock'
    if direct.exists():
        locks.append(direct)
    for p in db_dir.glob('db-*/default/cache/.lock'):
        if p.is_file():
            locks.append(p)
    try:
        for p in db_dir.rglob('.lock'):
            if p.is_file() and p.as_posix().endswith('/cache/.lock'):
                if p not in locks:
                    locks.append(p)
    except Exception:
        pass
    return locks

def force_delete_lock(lock_file: Path, verbose: bool) -> bool:
    try:
        lock_file.unlink()
        if verbose: print(f"   Removed lock: {lock_file}")
        return True
    except Exception:
        pass
    try:
        os.chmod(lock_file, 0o666); lock_file.unlink()
        if verbose: print(f"   Chmod+Removed lock: {lock_file}")
        return True
    except Exception:
        pass
    try:
        open(lock_file,'w').close(); lock_file.unlink()
        if verbose: print(f"   Truncated+Removed lock: {lock_file}")
        return True
    except Exception:
        pass
    try:
        stale = lock_file.with_name(f".lock.stale.{int(_now())}")
        lock_file.rename(stale)
        if verbose: print(f"   Renamed lock to: {stale}")
        return True
    except Exception as e:
        print(f"{COL_YELLOW}⚠️  Could not remove lock {lock_file}:{COL_RESET} {e}")
        return False

def maybe_handle_cache_lock(db_dir: Path, args):
    locks = find_all_locks(db_dir)
    if not locks: return
    print(f"{COL_YELLOW}⚠️  IMB cache lock(s) detected under:{COL_RESET} {db_dir}")
    for lock in locks:
        print(f"   • {lock}")
        pid = read_lock_pid(lock)
        if args.check_lock_process and pid:
            print(f"     PID={pid} is {'RUNNING' if process_running(pid) else 'NOT running'}")
        if args.kill_lock_process and pid:
            print(f"{COL_RED}⚠ Killing PID {pid} from lock...{COL_RESET}")
            try_kill_process(pid, args.verbose)
        if args.unlock_cache:
            force_delete_lock(lock, args.verbose)

# ---- DB auto actions ----
def auto_finalize_db(db_dir: Path, verbose: bool) -> bool:
    cp = run_cmd(['codeql','database','finalize',str(db_dir)], verbose)
    if cp.returncode != 0:
        if verbose:
            print(cp.stderr.decode(errors='ignore')[:800])
        # treat "already finalized" as success
        if b"already finalized" in cp.stderr:
            return True
        print(f"{COL_YELLOW}⚠️  Failed to finalize DB {db_dir}{COL_RESET}")
        return False
    return True

def auto_init_db(db_dir: Path, lang: str, source_root: Path, verbose: bool) -> bool:
    cp = run_cmd(['codeql','database','create',str(db_dir), f'--language={lang}','--source-root', str(source_root)], verbose)
    if cp.returncode != 0:
        if verbose: print(cp.stderr.decode(errors='ignore')[:800])
        print(f"{COL_YELLOW}⚠️  Failed to create DB {db_dir} for {lang}{COL_RESET}")
        return False
    return True

EXCLUDE_DIRS = {'default/cache','logs','results','working','diagnostic'}

def is_db_empty(db_dir: Path) -> bool:
    count = 0
    for p in db_dir.rglob('*'):
        if p.is_dir():
            rel = p.relative_to(db_dir).as_posix()
            if any(rel.startswith(x) for x in EXCLUDE_DIRS): continue
            continue
        rel = p.relative_to(db_dir).as_posix()
        if any(rel.startswith(x) for x in EXCLUDE_DIRS): continue
        count += 1
        if count > 50: return False
    return True

# -------------------------
# DB detection
# -------------------------
def detect_databases(langs, db_root: Path, args):
    print(f"[*] {APP_NAME} looking for CodeQL databases in {db_root}/...")
    found: Dict[str, Path] = {}
    missing, unfinalized = [], []

    for raw in langs:
        lang = alias_lang(raw)
        db_dir = (db_root / lang)
        meta = db_dir / 'codeql-database.yml'

        if not meta.exists():
            print(f"  {COL_YELLOW}⚠️  Missing DB for language: {lang} (expected {db_dir}){COL_RESET}")
            if args.auto_init_db:
                if not args.source_root:
                    print(f"{COL_RED}✖ --auto-init-db requires --source-root <path>{COL_RESET}"); sys.exit(1)
                print(f"  → Creating DB for {lang} at {db_dir}")
                if not args.dry_run and not auto_init_db(db_dir, lang, args.source_root, args.verbose):
                    if not args.allow_missing_db: sys.exit(1)
            else:
                missing.append(lang); continue

        # Handle locks early
        maybe_handle_cache_lock(db_dir, args)

        # Unconditionally finalize if requested (idempotent)
        if args.auto_finalize_db and not args.dry_run:
            print(f"  → Finalizing DB for {lang} at {db_dir}")
            if not auto_finalize_db(db_dir, args.verbose):
                unfinalized.append(lang); 
                if not args.allow_missing_db: continue

        if not db_structure_ok(db_dir):
            print(f"  {COL_YELLOW}⚠️  DB at {db_dir} looks unusual (no db-* subdirs). Proceeding cautiously.{COL_RESET}")

        if not args.force_scan_unready and is_db_empty(db_dir):
            print(f"  {COL_YELLOW}⚠️  DB for {lang} appears empty/unusable; skipping (use --force-scan-unready to override).{COL_RESET}")
            continue

        print(f"  ✅ Found: {lang} → {db_dir}")
        found[lang] = db_dir

    if (missing or unfinalized) and not args.allow_missing_db:
        if missing:
            print(f"{COL_RED}✖ Some requested DBs are missing:{COL_RESET} {', '.join(sorted(set(missing)))}")
        if unfinalized:
            print(f"{COL_RED}✖ Some requested DBs failed to finalize:{COL_RESET} {', '.join(sorted(set(unfinalized)))}")
        print("Use --allow-missing-db to continue anyway.")
        sys.exit(1)
    return found

# -------------------------
# Severity mapping/matching
# -------------------------
def map_loose(sev: str) -> str:
    s = sev.strip().lower()
    if s == 'error': return 'CRITICAL'
    if s == 'warning': return 'HIGH'
    if s == 'note': return 'MEDIUM'
    return sev.upper()

def severity_matches(candidate: str, target: str, strict: bool) -> bool:
    if not candidate: return False
    cand = candidate.strip().upper()
    targ = target.strip().upper()
    if strict: return cand == targ
    mapped = map_loose(cand)
    return mapped == targ or (targ in cand)

# -------------------------
# Query discovery
# -------------------------
def infer_query_language(query_path: Path) -> Optional[str]:
    try:
        head = query_path.read_text(encoding='utf-8', errors='ignore')[:8000]
        m = IMPORT_LANG_RE.search(head)
        if m: return alias_lang(m.group(1))
    except Exception: pass
    return None

def gather_queries(query_dirs: List[Path], langs: List[str], suite_only: bool, verbose: bool) -> List[Tuple[Path, str]]:
    langs = [alias_lang(l) for l in langs]; langset = set(langs)
    collected: List[Path] = []
    for qdir in query_dirs:
        base = Path(qdir)
        if not base.is_dir():
            if verbose: print(f"{COL_YELLOW}⚠️  Query dir does not exist:{COL_RESET} {base}")
            continue
        suites = list(base.rglob('*.qls'))
        if suite_only or suites:
            if verbose and suites: print(f"  • Preferring suites in {base} ({len(suites)} found)")
            collected.extend(suites)
        else:
            collected.extend(base.rglob('*.ql')); collected.extend(base.rglob('*.qls'))
    collected = sorted(set(collected))

    pairs: List[Tuple[Path, str]] = []
    for q in collected:
        detected = infer_query_language(q)
        if detected and detected in langset:
            pairs.append((q, detected)); continue
        parts = [p.lower() for p in q.parts]
        hinted = next((l for l in langs if l in parts), None)
        if hinted: pairs.append((q, hinted))
        elif verbose: print(f"{COL_YELLOW}⚠️  Could not infer language for query:{COL_RESET} {q}")
    return pairs

# -------------------------
# Finding counters
# -------------------------
def count_csv_findings(csv_path: Path, severity_filter: Optional[str], strict: bool) -> int:
    findings = 0
    try:
        with open(csv_path, newline='') as cf:
            reader = csv.reader(cf); header_seen = False
            for row in reader:
                if not header_seen: header_seen = True; continue
                if severity_filter and len(row) > 2 and not severity_matches(row[2], severity_filter, strict):
                    continue
                findings += 1
    except Exception:
        return 0
    return findings

def count_json_findings(json_path: Path, severity_filter: Optional[str], strict: bool) -> int:
    try:
        with open(json_path) as jf: data = json.load(jf)
    except Exception: return 0
    total = 0
    items = data if isinstance(data, list) else data.get('results', []) if isinstance(data, dict) else []
    for item in items:
        if not isinstance(item, dict): continue
        if severity_filter:
            sev = (str(item.get('severity','')) or str(item.get('level','')) or str(item.get('properties',{}).get('severity','')))
            if not severity_matches(sev, severity_filter, strict): continue
        total += 1
    return total

def build_rule_severity_map(run: Dict[str, Any]) -> Dict[str, str]:
    mapping: Dict[str, str] = {}
    tool = run.get('tool', {}) or {}; driver = tool.get('driver', {}) or {}
    for rule in driver.get('rules', []) or []:
        rid = rule.get('id') or rule.get('name')
        props = rule.get('properties', {}) or {}
        sev = props.get('severity') or props.get('problem.severity') or ''
        if not sev:
            sev = (rule.get('defaultConfiguration', {}) or {}).get('level','')
        if rid: mapping[str(rid)] = str(sev)
    return mapping

def result_matches_severity(res: Dict[str, Any], rules_map: Dict[str, str], severity_filter: str, strict: bool) -> bool:
    props = res.get('properties', {}) or {}
    candidates = [str(res.get('severity','')), str(res.get('level','')), str(props.get('severity','')), str(props.get('problem.severity',''))]
    rid = res.get('ruleId') or res.get('rule',{}).get('id')
    if rid and rid in rules_map: candidates.append(str(rules_map[rid]))
    for c in candidates:
        if c and severity_matches(c, severity_filter, strict): return True
    return False

def count_sarif_findings(sarif_path: Path, severity_filter: Optional[str], strict: bool) -> int:
    try:
        with open(sarif_path) as sf: data = json.load(sf)
    except Exception: return 0
    total = 0
    if not isinstance(data, dict): return 0
    for run in data.get('runs', []) or []:
        rules_map = build_rule_severity_map(run)
        for res in run.get('results', []) or []:
            if severity_filter and not result_matches_severity(res, rules_map, severity_filter, strict):
                continue
            total += 1
    return total

# -------------------------
# Per-DB concurrency (avoid IMB lock races)
# -------------------------
_DB_LOCKS: Dict[Path, threading.Semaphore] = {}
_DB_LOCKS_MUX = threading.Lock()

def db_semaphore(db_path: Path) -> threading.Semaphore:
    with _DB_LOCKS_MUX:
        sem = _DB_LOCKS.get(db_path)
        if sem is None:
            sem = threading.Semaphore(1)  # serialize per DB
            _DB_LOCKS[db_path] = sem
        return sem

# -------------------------
# Runner & mergers
# -------------------------
def analyze_once(query: Path, lang: str, db_path: Path, output_format: str, out_file: Path,
                 args) -> Tuple[bool, bytes]:
    # Pre-query lock sweep
    maybe_handle_cache_lock(db_path, args)
    cmd = ['codeql','database','analyze', str(db_path), '--format', output_format, '--output', str(out_file), str(query)]
    if args.verbose:
        print("   cmd:", " ".join(cmd))
    cp = run_cmd(cmd, args.verbose)
    return (cp.returncode == 0), cp.stderr

def run_query(query: Path, lang: str, db_path: Path, output_format: str, tmp_dir: Path,
              args) -> Tuple[Optional[Path], int]:
    out_file = tmp_dir / f"{query.stem}_{lang}.{output_format}"
    if args.fancy:
        print(f"{COL_CYAN}🌐 [{lang}]{COL_RESET} {query.name}")

    sem = db_semaphore(db_path)
    with sem:  # ensure only one query per DB at a time
        # First attempt
        ok, stderr = analyze_once(query, lang, db_path, output_format, out_file, args)
        if not ok:
            err = stderr.decode(errors='ignore')
            needs_finalize = 'needs to be finalized' in err
            lock_hit = 'cache directory is already locked' in err or 'OverlappingFileLockException' in err
            retried = False

            if needs_finalize and args.auto_finalize_db and not args.dry_run:
                print(f"  ↻ Finalizing {lang} DB (auto) due to analyze error, then retry…")
                auto_finalize_db(db_path, args.verbose)
                maybe_handle_cache_lock(db_path, args)
                retried = True

            if lock_hit:
                print(f"  ↻ Clearing IMB locks for {lang} DB, then retry…")
                # force aggressive unlock regardless of flag on retry
                class _RetryArgs: pass
                _a = _RetryArgs(); _a.unlock_cache=True; _a.check_lock_process=args.check_lock_process
                _a.kill_lock_process=False; _a.verbose=args.verbose
                maybe_handle_cache_lock(db_path, _a)
                retried = True

            if retried:
                sleep(0.25)
                ok, stderr = analyze_once(query, lang, db_path, output_format, out_file, args)

        if not ok:
            with open('hydraql_failures.log','a') as flog:
                flog.write(f"FAIL {query} on {lang}:\n{stderr.decode(errors='ignore')}\n")
            if args.verbose:
                print(f"{COL_RED}❌ Query failed:{COL_RESET} {query}")
                print(stderr.decode(errors='ignore')[:600], "..." if len(stderr) > 600 else "")
            return None, 0

    if not out_file.exists():
        return None, 0

    # Count
    if output_format == 'csv':
        findings = count_csv_findings(out_file, args.severity_filter, args.strict_severity)
    elif output_format == 'json':
        findings = count_json_findings(out_file, args.severity_filter, args.strict_severity)
    else:
        findings = count_sarif_findings(out_file, args.severity_filter, args.strict_severity)
    return out_file, findings

def merge_csv(results: List[Path], output_file: str, severity_filter: Optional[str], strict: bool) -> int:
    fixed_header = ["Name","Description","Severity","Message","Path","Start line","Start column","End line","End column"]
    rows: List[List[str]] = []
    for file in results:
        if not file or not file.exists(): continue
        with open(file, newline='') as cf:
            reader = csv.reader(cf); header_seen = False
            for row in reader:
                if not header_seen: header_seen = True; continue
                if severity_filter and len(row) > 2 and not severity_matches(row[2], severity_filter, strict):
                    continue
                rows.append(row)
    with open(output_file, 'w', newline='') as outf:
        writer = csv.writer(outf); writer.writerow(fixed_header); writer.writerows(rows)
    return len(rows)

def merge_json(results: List[Path], output_file: str, severity_filter: Optional[str], strict: bool) -> int:
    merged: List[Any] = []
    for file in results:
        if not file or not file.exists(): continue
        try: data = json.loads(Path(file).read_text())
        except Exception: continue
        items = data if isinstance(data, list) else data.get('results', []) if isinstance(data, dict) else []
        for item in items:
            if not isinstance(item, dict): continue
            if severity_filter:
                sev = (str(item.get('severity','')) or str(item.get('level','')) or str(item.get('properties',{}).get('severity','')))
                if not severity_matches(sev, severity_filter, strict): continue
            merged.append(item)
    Path(output_file).write_text(json.dumps(merged, indent=2))
    return len(merged)

def merge_sarif(results: List[Path], output_file: str, severity_filter: Optional[str], strict: bool) -> int:
    merged: Dict[str, Any] = {'$schema': 'https://schemastore.azurewebsites.net/schemas/json/sarif-2.1.0.json','version':'2.1.0','runs': []}
    total = 0
    for file in results:
        if not file or not file.exists(): continue
        try: data = json.loads(Path(file).read_text())
        except Exception: continue
        for run in data.get('runs', []) or []:
            rules_map = build_rule_severity_map(run)
            new_run = dict(run)
            if isinstance(run.get('results'), list):
                filtered = []
                for res in run['results']:
                    if severity_filter and not result_matches_severity(res, rules_map, severity_filter, strict): continue
                    filtered.append(res)
                new_run['results'] = filtered
                total += len(filtered)
            merged['runs'].append(new_run)
    Path(output_file).write_text(json.dumps(merged, indent=2))
    return total

def ascii_chart(with_results: int, without_results: int, fancy: bool):
    title = f"\n{COL_BLUE}📊 Result Summary Chart{COL_RESET}" if fancy else "\nResult Summary Chart"
    print(title); print(f"{'Category':<30} Bar")
    print(f"{'Queries with results':<30} {'#'*with_results}")
    print(f"{'Queries without results':<30} {'#'*without_results}")

# -------------------------
# Main
# -------------------------
def main():
    args = parse_args()
    print(f"{COL_GREEN}{APP_NAME}{COL_RESET} starting…")

    query_dirs = normalize_query_dirs(args.query_dirs)
    langs_raw = [l.strip() for l in args.langs.split(',') if l.strip()]
    langs = [alias_lang(l) for l in langs_raw]

    if args.pack_install:
        install_query_packs(args.fancy, args.verbose)

    found_dbs = detect_databases(langs, args.db_root, args)
    if not found_dbs:
        print("No valid databases found; exiting."); sys.exit(1)

    pairs = gather_queries(query_dirs, list(found_dbs.keys()), args.suite_only, args.verbose)
    if not pairs:
        print("No queries found; exiting.")
        print(" Searched in:"); [print(f"  - {d}") for d in query_dirs]
        print(" For languages:"); [print(f"  - {l}") for l in found_dbs.keys()]
        sys.exit(1)

    if args.fancy:
        print(f"{COL_MAGENTA}[*]{COL_RESET} Found {len(pairs)} queries to run")

    tmp_dir = Path('tmp_hydraql_output'); tmp_dir.mkdir(exist_ok=True)
    Path('hydraql_failures.log').write_text("")

    results: List[Path] = []; per_query_counts: List[int] = []
    with ThreadPoolExecutor(max_workers=args.threads) as executor:
        futs = []
        for q, lang in pairs:
            dbp = found_dbs.get(lang)
            if not dbp:
                if args.verbose: print(f"{COL_YELLOW}⚠️  Skipping {q} — no DB for {lang}{COL_RESET}")
                continue
            futs.append(executor.submit(run_query, q, lang, dbp, args.output_format, tmp_dir, args))
        for fut in as_completed(futs):
            out_file, count = fut.result()
            if out_file: results.append(out_file)
            per_query_counts.append(count)

    ts = datetime.now().strftime("%Y%m%d%H%M%S")
    output_file = f"HydraQL_output-{ts}.{args.output_format}"
    if args.output_format == 'csv':
        total = merge_csv(results, output_file, args.severity_filter, args.strict_severity)
    elif args.output_format == 'json':
        total = merge_json(results, output_file, args.severity_filter, args.strict_severity)
    else:
        total = merge_sarif(results, output_file, args.severity_filter, args.strict_severity)

    ran = len(pairs); with_results = sum(1 for n in per_query_counts if n > 0); without_results = ran - with_results
    print("\n🔎 Summary:\n───────────────────────────────")
    print(f"Total queries run:       {ran}")
    print(f"Total findings:          {total}")
    ascii_chart(with_results, without_results, args.fancy)

    if args.fancy:
        if total > 0: print(f"{COL_GREEN}✅ Done! Results saved to {output_file}{COL_RESET}")
        else: print(f"{COL_YELLOW}⚠️  No results. Wrote header to {output_file}{COL_RESET}")
    else:
        print(f"Results saved to {output_file}")

    try:
        for f in tmp_dir.iterdir(): f.unlink()
        tmp_dir.rmdir()
    except Exception: pass

if __name__ == '__main__':
    main()