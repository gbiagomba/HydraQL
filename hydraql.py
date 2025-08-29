#!/usr/bin/env python3
"""
HydraQL — Parallel CodeQL Scanning Engine
----------------------------------------
Multi-DB, multi-language, parallel CodeQL runner with:
- Multiple query dirs (repeatable or comma-separated; trims whitespace, expands ~)
- Language aliasing (TypeScript→JavaScript, Kotlin→Java)
- Robust DB preflight (presence + finalized + structure check)
- Parallel execution with per-query outputs, merged into CSV/JSON/SARIF
- Severity filtering, fancy colored output, dry run, auto `codeql pack install`
- ASCII summary chart
"""
import argparse
import subprocess
import sys
import csv
import json
import os
import re
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime

# ANSI color codes
COL_RESET = "\033[0m"
COL_CYAN = "\033[1;36m"
COL_GREEN = "\033[1;32m"
COL_YELLOW = "\033[1;33m"
COL_MAGENTA = "\033[1;35m"
COL_BLUE = "\033[1;34m"
COL_RED = "\033[1;31m"

APP_NAME = "HydraQL"

LANG_ALIASES = {
    'typescript': 'javascript',
    'kotlin': 'java',
}
SUPPORTED_LANGS = { 'java','javascript','python','cpp','swift','ruby' }

IMPORT_LANG_RE = re.compile(r"^\s*import\s+(java|javascript|python|cpp|swift|ruby|kotlin|typescript)\b", re.I | re.M)


def parse_args():
    p = argparse.ArgumentParser(description=f"{APP_NAME} — Parallel CodeQL scanning")
    p.add_argument('--dir', dest='scan_dir', type=Path, default=Path.home()/ 'Git', help='(Reserved) Source dir')
    p.add_argument('--langs', default='java,javascript,typescript,python', help='Comma-separated languages')
    p.add_argument('--db-root', dest='db_root', type=Path, default=Path('cqlDB'), help='Root of CodeQL databases')
    p.add_argument('--query-dir', dest='query_dirs', action='append', help='Query dir(s); repeat or comma-separate')
    p.add_argument('--threads','--parallel', dest='threads', type=int, default=6, help='Parallel jobs')
    p.add_argument('--output-format', dest='output_format', choices=['csv','json','sarif'], default='csv')
    p.add_argument('--severity', dest='severity_filter', default=None, help='Filter by severity (e.g., HIGH)')
    p.add_argument('--fancy', action='store_true', help='Colored output + ASCII chart')
    p.add_argument('--dry-run', action='store_true', help='List actions without running CodeQL')
    p.add_argument('--pack-install', action='store_true', help='Run `codeql pack install` before scanning')
    p.add_argument('--allow-missing-db', action='store_true', help='Proceed even if some requested DBs are missing or unfinalized')
    return p.parse_args()


def normalize_query_dirs(raw_dirs):
    if not raw_dirs:
        return [ (Path.home() / 'Git' / 'codeql').expanduser() ]
    out = []
    for entry in raw_dirs:
        for token in entry.split(','):
            tok = token.strip()
            if not tok:
                continue
            out.append(Path(tok).expanduser())
    return out


def install_query_packs(fancy):
    if fancy: print(f"{COL_MAGENTA}[*]{COL_RESET} Installing CodeQL query packs...")
    try:
        subprocess.run(['codeql','pack','install'], check=True)
    except subprocess.CalledProcessError:
        print(f"{COL_YELLOW}⚠️  Failed to install query packs{COL_RESET}")


def alias_lang(lang: str) -> str:
    return LANG_ALIASES.get(lang.lower(), lang.lower())


def db_structure_ok(db_dir: Path) -> bool:
    """Heuristic: a CodeQL DB typically has subdirs like db-<lang> and codeql-database.yml"""
    if not (db_dir / 'codeql-database.yml').exists():
        return False
    has_sub = any(child.is_dir() and child.name.startswith('db-') for child in db_dir.iterdir())
    return has_sub


def detect_databases(langs, db_root: Path, fancy: bool, allow_missing: bool):
    print(f"[*] {APP_NAME} looking for CodeQL databases in {db_root}/...")
    found = {}
    missing = []
    unfinalized = []
    for raw in langs:
        lang = alias_lang(raw)
        db_dir = (db_root / lang)
        meta = db_dir / 'codeql-database.yml'
        if not meta.exists():
            print(f"  {COL_YELLOW}⚠️  Missing DB for language: {lang} (expected {db_dir}){COL_RESET}")
            missing.append(lang)
            continue
        text = meta.read_text(encoding='utf-8', errors='ignore')
        if 'finalized: false' in text:
            print(f"  {COL_RED}❌ DB not finalized for {lang}. Run: codeql database finalize {db_dir}{COL_RESET}")
            unfinalized.append(lang)
            continue
        if not db_structure_ok(db_dir):
            print(f"  {COL_YELLOW}⚠️  DB at {db_dir} looks unusual (no db-* subdirs). Proceeding cautiously.{COL_RESET}")
        print(f"  ✅ Found: {lang} → {db_dir}")
        found[lang] = db_dir
    if (missing or unfinalized) and not allow_missing:
        if missing:
            print(f"{COL_RED}✖ Some requested DBs are missing:{COL_RESET} {', '.join(sorted(set(missing)))}")
        if unfinalized:
            print(f"{COL_RED}✖ Some requested DBs are unfinalized:{COL_RESET} {', '.join(sorted(set(unfinalized)))}")
        print("Use --allow-missing-db to continue anyway.")
        sys.exit(1)
    return found


def infer_query_language(query_path: Path) -> str | None:
    try:
        head = query_path.read_text(encoding='utf-8', errors='ignore')[:4000]
        m = IMPORT_LANG_RE.search(head)
        if not m:
            return None
        return alias_lang(m.group(1))
    except Exception:
        return None


def gather_queries(query_dirs, langs):
    langs = [alias_lang(l) for l in langs]
    langset = set(langs)
    all_queries = []
    for qdir in query_dirs:
        base = Path(qdir)
        if not base.is_dir():
            continue
        all_queries.extend(base.rglob('*.ql'))
        all_queries.extend(base.rglob('*.qls'))
    # dedupe
    all_queries = sorted(set(all_queries))
    pairs = []  # (query_path, lang)
    for q in all_queries:
        detected = infer_query_language(q)
        if detected and detected in langset:
            pairs.append((q, detected))
            continue
        parts = [p.lower() for p in q.parts]
        hinted = next((l for l in langs if l in parts), None)
        if hinted:
            pairs.append((q, hinted))
    return pairs


def run_query(query: Path, lang: str, db_path: Path, output_format: str, tmp_dir: Path, dry_run: bool, fancy: bool):
    out_file = tmp_dir / f"{query.stem}_{lang}.{output_format}"
    cmd = ['codeql','database','analyze', str(db_path), '--format', output_format, '--output', str(out_file), str(query)]
    if fancy:
        print(f"{COL_CYAN}🌐 [{lang}]{COL_RESET} Running query: {query.name}")
    if dry_run:
        return None
    try:
        subprocess.run(cmd, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    except subprocess.CalledProcessError:
        return None
    return out_file if out_file.exists() else None


def merge_csv(results, output_file, severity_filter):
    header = None
    rows = []
    for file in results:
        if not file or not file.exists():
            continue
        with open(file, newline='') as cf:
            reader = csv.reader(cf)
            try:
                hdr = next(reader)
            except StopIteration:
                continue
            if header is None:
                header = hdr
            for row in reader:
                if severity_filter:
                    if len(row) > 2 and row[2].strip().upper() != severity_filter.upper():
                        continue
                rows.append(row)
    if header is None:
        header = ["Name","Description","Severity","Message","Path","Start line","Start column","End line","End column"]
    with open(output_file, 'w', newline='') as outf:
        writer = csv.writer(outf)
        writer.writerow(header)
        writer.writerows(rows)
    return len(rows)


def merge_json(results, output_file, severity_filter):
    merged = []
    for file in results:
        if not file or not file.exists():
            continue
        with open(file) as jf:
            try:
                data = json.load(jf)
            except Exception:
                continue
        if isinstance(data, list):
            for item in data:
                if severity_filter and isinstance(item, dict):
                    if str(item.get('severity','')).upper() != severity_filter.upper():
                        continue
                merged.append(item)
        else:
            merged.append(data)
    with open(output_file, 'w') as outf:
        json.dump(merged, outf, indent=2)
    return len(merged)


def merge_sarif(results, output_file):
    merged = { '$schema': 'https://schemastore.azurewebsites.net/schemas/json/sarif-2.1.0.json', 'version': '2.1.0', 'runs': [] }
    for file in results:
        if not file or not file.exists():
            continue
        with open(file) as sf:
            try:
                data = json.load(sf)
            except Exception:
                continue
        if 'runs' in data:
            merged['runs'].extend(data['runs'])
    with open(output_file, 'w') as outf:
        json.dump(merged, outf, indent=2)
    total = sum(len(run.get('results', [])) for run in merged['runs'])
    return total


def ascii_chart(with_results, without_results, fancy):
    title = f"\n{COL_BLUE}📊 Result Summary Chart{COL_RESET}" if fancy else "\nResult Summary Chart"
    print(title)
    print(f"{'Category':<30} Bar")
    print(f"{'Queries with results':<30} {'#'*with_results}")
    print(f"{'Queries without results':<30} {'#'*without_results}")


def main():
    args = parse_args()
    print(f"{COL_GREEN}{APP_NAME}{COL_RESET} starting…")

    query_dirs = normalize_query_dirs(args.query_dirs)
    langs_raw = [l.strip() for l in args.langs.split(',') if l.strip()]
    langs = [alias_lang(l) for l in langs_raw]

    if args.pack_install:
        install_query_packs(args.fancy)

    found_dbs = detect_databases(langs, args.db_root, args.fancy, args.allow_missing_db)
    if not found_dbs:
        print("No valid databases found; exiting.")
        sys.exit(1)

    pairs = gather_queries(query_dirs, found_dbs.keys())
    if not pairs:
        print("No queries found; exiting.")
        print(" Searched in:")
        for d in query_dirs:
            print(f"  - {d}")
        print(" For languages:")
        for l in found_dbs.keys():
            print(f"  - {l}")
        sys.exit(1)

    if args.fancy:
        print(f"{COL_MAGENTA}[*]{COL_RESET} Found {len(pairs)} queries to run")

    tmp_dir = Path('tmp_hydraql_output')
    tmp_dir.mkdir(exist_ok=True)

    # Run in parallel
    results = []
    with ThreadPoolExecutor(max_workers=args.threads) as executor:
        futs = []
        for q, lang in pairs:
            dbp = found_dbs.get(lang)
            if not dbp:
                continue
            futs.append(executor.submit(run_query, q, lang, dbp, args.output_format, tmp_dir, args.dry_run, args.fancy))
        for fut in as_completed(futs):
            out = fut.result()
            if out:
                results.append(out)

    # Merge outputs
    timestamp = datetime.now().strftime("%Y%m%d%H%M%S")
    output_file = f"HydraQL_output-{timestamp}.{args.output_format}"
    if args.output_format == 'csv':
        total = merge_csv(results, output_file, args.severity_filter)
    elif args.output_format == 'json':
        total = merge_json(results, output_file, args.severity_filter)
    else:
        total = merge_sarif(results, output_file)

    ran = len(pairs)
    with_results = 1 if total > 0 else 0
    without_results = ran - with_results

    print("\n🔎 Summary:")
    print("───────────────────────────────")
    print(f"Total queries run:       {ran}")
    print(f"Total findings:          {total}")
    ascii_chart(with_results, without_results, args.fancy)

    msg = f"✅ Done! Results saved to {output_file}" if total else f"⚠️  No results. Wrote empty template to {output_file}"
    if args.fancy:
        color = COL_GREEN if total else COL_YELLOW
        print(f"{color}{msg}{COL_RESET}")
    else:
        print(msg)

    # Cleanup
    try:
        for f in tmp_dir.iterdir():
            f.unlink()
        tmp_dir.rmdir()
    except Exception:
        pass

if __name__ == '__main__':
    main()
