#!/bin/bash

set -euo pipefail

# Defaults
SCAN_DIR="$HOME/Git"
LANGS="java,javascript,typescript,python"
CODEQL_DB_ROOT="cqlDB"
THREADS=6

# Parse args
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir)
      SCAN_DIR="$2"
      shift 2
      ;;
    --langs)
      LANGS="$2"
      shift 2
      ;;
    --threads)
      THREADS="$2"
      shift 2
      ;;
    *)
      echo "Unknown option: $1"
      exit 1
  esac
done

# Normalize
IFS=',' read -ra LANG_ARRAY <<< "$LANGS"

# Generate timestamp
current_time=$(date +%Y%m%d%H%M%S)
header="Name,Description,Severity,Message,Path,Start line,Start column,End line,End column"
echo "$header" > codeql_output-$current_time.csv
cp codeql_output-$current_time.csv codeql_output-$current_time.bkup.csv

mkdir -p tmp_codeql_csvs
> tmp_query_list.txt

# Build query list dynamically
echo "[*] Building query list from $SCAN_DIR..."
for lang in "${LANG_ARRAY[@]}"; do
  find "$SCAN_DIR" -type f \
    | grep -Ei "$lang" \
    | grep -Ei "security|cwe|cve|ghsl|weak|unused|duplicate|null" \
    | grep -Ei "\.qls$|\.ql$" \
    | grep -Eiv "\.qll$|\.qlref$|test/" \
    >> tmp_query_list.txt
done

# Auto-detect available databases
declare -A DB_MAP
echo "[*] Detecting CodeQL databases in $CODEQL_DB_ROOT/..."
for lang in "${LANG_ARRAY[@]}"; do
  if [[ -d "$CODEQL_DB_ROOT/$lang" ]]; then
    DB_MAP["$lang"]="$CODEQL_DB_ROOT/$lang"
    echo "  ? Found: $lang at ${DB_MAP[$lang]}"
  else
    echo "  ? Missing CodeQL DB for language: $lang (skipping)"
  fi
done

# Function for parallel execution
run_query() {
  local query="$1"
  local lang
  lang=$(echo "$query" | grep -Eio 'java|javascript|typescript|python' | head -n1)

  db_path="${DB_MAP[$lang]}"
  if [[ -z "$db_path" ]]; then
    echo "??  Skipping $query Ñ no DB found for language $lang"
    return
  fi

  out_file="tmp_codeql_csvs/$(basename "$query" | sed 's/\.[^.]*$//')_${lang}.csv"

  echo "[+] [$lang] Running: $query"
  codeql database analyze "$db_path" \
    --format=csv \
    --output="$out_file" \
    "$query" 2>/dev/null

  if [[ -f "$out_file" ]]; then
    tail -n +2 "$out_file" >> codeql_output-$current_time.bkup.csv
  fi
}
export -f run_query
export DB_MAP
export CODEQL_DB_ROOT

# Run everything in parallel
cat tmp_query_list.txt | xargs -n 1 -P "$THREADS" -I{} bash -c 'run_query "$@"' _ {}

# Clean up and view
rm -rf tmp_query_list.txt tmp_codeql_csvs
vim codeql_output-$current_time.bkup.csv