#!/bin/bash

set -euo pipefail

# === Defaults ===
SCAN_DIR="$HOME/Git"
LANGS="java,javascript,typescript,python"
CODEQL_DB_ROOT="cqlDB"
THREADS=6
OUTPUT_FORMAT="csv"
QUERY_DIRS=()
FANCY=false
DRY_RUN=false
FAIL_LOG="codeql_failures.log"
> "$FAIL_LOG"

# === Summary Counters ===
QUERIES_RUN=0
QUERIES_WITH_RESULTS=0
QUERIES_WITHOUT_RESULTS=0

# === Help ===
print_help() {
  cat <<EOF
Usage: $0 [options]

Options:
  --dir <path>            Directory to scan (default: $HOME/Git)
  --langs <list>          Comma-separated list of languages (default: java,javascript,typescript,python)
  --threads <num>         Number of parallel jobs (default: 6)
  --output-format <fmt>   Output format: csv, json, or sarif (default: csv)
  --query-dir <path>      Directory where CodeQL queries live (can be repeated or comma-separated)
  --db-root <path>        Directory containing language databases (default: ./cqlDB)
  --fancy                 Enable fancy colored output 🌈
  --dry-run               Print what would run without executing
  --help                  Show this help message and exit

Example:
  $0 --db-root ./cqlDB_prj-high5 --langs javascript,typescript --query-dir ~/Git/codeql --output-format sarif --fancy
EOF
  exit 0
}

# === Parse arguments ===
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir) SCAN_DIR="$2"; shift 2 ;;
    --langs) LANGS="$2"; shift 2 ;;
    --threads) THREADS="$2"; shift 2 ;;
    --output-format) OUTPUT_FORMAT="$2"; shift 2 ;;
    --query-dir)
      IFS=',' read -ra SPLIT <<< "$2"
      for dir in "${SPLIT[@]}"; do QUERY_DIRS+=("$dir"); done
      shift 2 ;;
    --db-root) CODEQL_DB_ROOT="$2"; shift 2 ;;
    --fancy) FANCY=true; shift ;;
    --dry-run) DRY_RUN=true; shift ;;
    --help) print_help ;;
    *) echo "Unknown option: $1"; print_help ;;
  esac
done

# === Validate output format ===
if [[ ! "$OUTPUT_FORMAT" =~ ^(csv|json|sarif)$ ]]; then
  echo "❌ Invalid output format: $OUTPUT_FORMAT"
  echo "Valid formats: csv, json, sarif"
  exit 1
fi

IFS=',' read -ra LANG_ARRAY <<< "$LANGS"

# === Output setup ===
current_time=$(date +%Y%m%d%H%M%S)
OUTPUT_FILE="codeql_output-$current_time.$OUTPUT_FORMAT"
TMP_DIR="tmp_codeql_output"
mkdir -p "$TMP_DIR"

[[ "$OUTPUT_FORMAT" == "csv" ]] && echo "Name,Description,Severity,Message,Path,Start line,Start column,End line,End column" > "$OUTPUT_FILE"

# === Detect CodeQL databases ===
echo "[*] Looking for CodeQL databases in $CODEQL_DB_ROOT/..."
FOUND_LANGS=() || true
for lang in "${LANG_ARRAY[@]}"; do
  if [[ -d "$CODEQL_DB_ROOT/$lang" ]]; then
    FOUND_LANGS+=("$lang")
    echo "  ✅ Found: $lang → $CODEQL_DB_ROOT/$lang"
  else
    echo "  ⚠️  Missing DB for language: $lang (will skip)"
  fi
done

# === Build query list ===
> tmp_query_list.txt
echo "[*] Searching for queries in: ${QUERY_DIRS[*]}"
for dir in "${QUERY_DIRS[@]}"; do
  for lang in "${FOUND_LANGS[@]}"; do
    [[ -d "$dir" ]] && find "$dir" -type f \( -name "*.ql" -o -name "*.qls" \) | grep -Ei "/$lang/" >> tmp_query_list.txt
  done
done

if [[ ! -s tmp_query_list.txt ]]; then
  echo "❌ No queries found. Please check your --query-dir and --langs filters."
  exit 1
fi

$FANCY && echo -e "\n\033[1;35m✨ Dry run mode: $DRY_RUN\033[0m"
echo "[*] Queries to run:"
cat tmp_query_list.txt

# === Run query ===
run_query() {
  local query="$1"
  local lang db_path base out_file row_count
  lang=$(echo "$query" | grep -Eio 'java|javascript|typescript|python' | head -n1)
  db_path="$CODEQL_DB_ROOT/$lang"

  [[ ! -d "$db_path" ]] && echo "❌ Skipping $query — no DB for $lang" && return
  base=$(basename "$query" | sed 's/\.[^.]*$//')
  out_file="$TMP_DIR/${base}_${lang}.${OUTPUT_FORMAT}"
  ((QUERIES_RUN++))

  if $FANCY; then
    echo -e "\033[1;36m🌐 [$lang]\033[0m Running query: $base"
  else
    echo "[+] Running: $query"
  fi

  if $DRY_RUN; then return; fi

  if ! codeql database analyze "$db_path" \
    --format="$OUTPUT_FORMAT" \
    --output="$out_file" \
    "$query" 2>>"$FAIL_LOG"; then
    echo "⚠️  Query failed: $query" >> "$FAIL_LOG"
    return
  fi

  if [[ "$OUTPUT_FORMAT" == "csv" && -s "$out_file" ]]; then
    row_count=$(wc -l < "$out_file")
    if [[ "$row_count" -gt 1 ]]; then
      tail -n +2 "$out_file" >> "$TMP_DIR/all_results.csv"
      ((QUERIES_WITH_RESULTS++))
    else
      ((QUERIES_WITHOUT_RESULTS++))
    fi
  fi
}
export -f run_query
export CODEQL_DB_ROOT OUTPUT_FORMAT TMP_DIR QUERIES_RUN QUERIES_WITH_RESULTS QUERIES_WITHOUT_RESULTS FAIL_LOG FANCY DRY_RUN

# === Run in parallel ===
cat tmp_query_list.txt | xargs -n 1 -P "$THREADS" -I{} bash -c 'run_query "$@"' _ {}

# === Merge output ===
if [[ "$OUTPUT_FORMAT" == "csv" ]]; then
  echo "Name,Description,Severity,Message,Path,Start line,Start column,End line,End column" > "$OUTPUT_FILE"
  if [[ -f "$TMP_DIR/all_results.csv" ]]; then
    cat "$TMP_DIR/all_results.csv" >> "$OUTPUT_FILE"
  else
    echo "⚠️  No results were found. CSV output only contains the header."
  fi
fi

# === Cleanup ===
rm -rf "$TMP_DIR" tmp_query_list.txt

# === Summary ===
echo
echo "🔎 Summary:"
echo "───────────────────────────────"
echo "Total queries run:       $QUERIES_RUN"
echo "Queries with results:    $QUERIES_WITH_RESULTS"
echo "Queries without results: $QUERIES_WITHOUT_RESULTS"
[[ -s "$FAIL_LOG" ]] && echo "❌ Failures logged in:    $FAIL_LOG"

if $FANCY; then
  if [[ "$QUERIES_WITH_RESULTS" -gt 0 ]]; then
    echo -e "\033[1;32m✅ Success: Results saved to $OUTPUT_FILE\033[0m"
  else
    echo -e "\033[1;33m⚠️  No results found. Check your queries or DBs.\033[0m"
  fi
else
  echo "Results saved to $OUTPUT_FILE"
fi
