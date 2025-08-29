#!/bin/bash

set -euo pipefail

# === Default values ===
SCAN_DIR="$HOME/Git"
LANGS="java,javascript,typescript,python"
CODEQL_DB_ROOT="cqlDB"
THREADS=6
OUTPUT_FORMAT="csv"
QUERY_DIRS=()

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
  --db-root <path>        Root directory for CodeQL databases (default: cqlDB)
  --help                  Show this help message and exit

Example:
  $0 --dir ./ --langs javascript,typescript --query-dir ~/Git/codeql --query-dir ~/Git/custom-queries --output-format csv
EOF
  exit 0
}

# === Parse args ===
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir) SCAN_DIR="$2"; shift 2 ;;
    --langs) LANGS="$2"; shift 2 ;;
    --threads) THREADS="$2"; shift 2 ;;
    --output-format) OUTPUT_FORMAT="$2"; shift 2 ;;
    --query-dir)
      IFS=',' read -ra SPLIT <<< "$2"
      for dir in "${SPLIT[@]}"; do
        QUERY_DIRS+=("$dir")
      done
      shift 2
      ;;
    --db-root)
      CODEQL_DB_ROOT="$2"
      shift 2
      ;;
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

if [[ "$OUTPUT_FORMAT" == "csv" ]]; then
  echo "Name,Description,Severity,Message,Path,Start line,Start column,End line,End column" > "$OUTPUT_FILE"
fi

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
    if [[ -d "$dir" ]]; then
      find "$dir" -type f \( -name "*.ql" -o -name "*.qls" \) \
        | grep -Ei "/$lang/" \
        >> tmp_query_list.txt
    fi
  done
done

if [[ ! -s tmp_query_list.txt ]]; then
  echo "❌ No queries found. Please check your --query-dir and --langs filters."
  exit 1
fi

echo "[*] Queries to run:"
cat tmp_query_list.txt

# === Query execution function ===
run_query() {
  local query="$1"
  local lang
  lang=$(echo "$query" | grep -Eio 'java|javascript|typescript|python' | head -n1)
  local db_path="$CODEQL_DB_ROOT/$lang"

  if [[ ! -d "$db_path" ]]; then
    echo "❌ Skipping $query — no DB found for $lang"
    return
  fi

  local base
  base=$(basename "$query" | sed 's/\.[^.]*$//')
  local out_file="$TMP_DIR/${base}_${lang}.${OUTPUT_FORMAT}"

  echo "[+] Running: $query → DB: $db_path"
  codeql database analyze "$db_path" \
    --format="$OUTPUT_FORMAT" \
    --output="$out_file" \
    "$query" 2>/dev/null

  if [[ -f "$out_file" && "$OUTPUT_FORMAT" == "csv" ]]; then
    tail -n +2 "$out_file" >> "$TMP_DIR/all_results.csv"
  fi
}
export -f run_query
export CODEQL_DB_ROOT
export OUTPUT_FORMAT
export TMP_DIR

# === Parallel execution ===
cat tmp_query_list.txt | xargs -n 1 -P "$THREADS" -I{} bash -c 'run_query "$@"' _ {}

# === Combine final output ===
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

echo "✅ Scan complete. Results saved to $OUTPUT_FILE"