#!/bin/bash

set -euo pipefail

# Defaults
SCAN_DIR="$HOME/Git/codeql"
LANGS="java,javascript,typescript,python"
CODEQL_DB_ROOT="cqlDB"
THREADS=6
OUTPUT_FORMAT="csv"

print_help() {
  cat <<EOF
Usage: $0 [options]

Options:
  --dir <path>            Directory to scan (default: $HOME/Git)
  --langs <list>          Comma-separated list of languages (default: java,javascript,typescript,python)
  --threads <num>         Number of parallel jobs (default: 6)
  --output-format <fmt>   Output format: csv, json, or sarif (default: csv)
  --help                  Show this help message and exit

Example:
  $0 --dir ~/Projects --langs java,python --output-format sarif
EOF
  exit 0
}

# Parse args
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir)
      SCAN_DIR="$2"; shift 2 ;;
    --langs)
      LANGS="$2"; shift 2 ;;
    --threads)
      THREADS="$2"; shift 2 ;;
    --output-format)
      OUTPUT_FORMAT="$2"; shift 2 ;;
    --help)
      print_help ;;
    *)
      echo "Unknown option: $1"
      print_help ;;
  esac
done

# Validate output format
if [[ ! "$OUTPUT_FORMAT" =~ ^(csv|json|sarif)$ ]]; then
  echo "❌ Invalid output format: $OUTPUT_FORMAT"
  echo "Valid formats: csv, json, sarif"
  exit 1
fi

# Normalize inputs
IFS=',' read -ra LANG_ARRAY <<< "$LANGS"

# Setup output
current_time=$(date +%Y%m%d%H%M%S)
OUTPUT_FILE="codeql_output-$current_time.$OUTPUT_FORMAT"
TMP_DIR="tmp_codeql_output"
mkdir -p "$TMP_DIR"

if [[ "$OUTPUT_FORMAT" == "csv" ]]; then
  echo "Name,Description,Severity,Message,Path,Start line,Start column,End line,End column" > "$OUTPUT_FILE"
fi

# Build query list
> tmp_query_list.txt
for lang in "${LANG_ARRAY[@]}"; do
  find "$SCAN_DIR" -type f \
    | grep -Ei "$lang" \
    | grep -Ei "security|cwe|cve|ghsl|weak|unused|duplicate|null" \
    | grep -Ei "\.qls$|\.ql$" \
    | grep -Eiv "\.qll$|\.qlref$|test/" \
    >> tmp_query_list.txt
done

# Detect databases
declare -A DB_MAP
for lang in "${LANG_ARRAY[@]}"; do
  if [[ -d "$CODEQL_DB_ROOT/$lang" ]]; then
    DB_MAP["$lang"]="$CODEQL_DB_ROOT/$lang"
    echo "✅ Found DB: $lang → ${DB_MAP[$lang]}"
  else
    echo "⚠️ Missing DB for: $lang (skipping scans for this language)"
  fi
done

# Function for parallel query execution
run_query() {
  local query="$1"
  local lang
  lang=$(echo "$query" | grep -Eio 'java|javascript|typescript|python' | head -n1)
  local db="${DB_MAP[$lang]}"

  if [[ -z "$db" ]]; then
    echo "❌ Skipping $query — no DB found for $lang"
    return
  fi

  local base=$(basename "$query" | sed 's/\.[^.]*$//')
  local tmp_file="$TMP_DIR/${base}_${lang}.${OUTPUT_FORMAT}"

  echo "[+] Running: $query on $lang"
  codeql database analyze "$db" \
    --format="$OUTPUT_FORMAT" \
    --output="$tmp_file" \
    "$query" 2>/dev/null

  if [[ "$OUTPUT_FORMAT" == "csv" && -f "$tmp_file" ]]; then
    tail -n +2 "$tmp_file" >> "$OUTPUT_FILE"
  elif [[ "$OUTPUT_FORMAT" != "csv" && -f "$tmp_file" ]]; then
    echo "," >> "$OUTPUT_FILE" # slight delimiter for later processing
    cat "$tmp_file" >> "$OUTPUT_FILE"
  fi
}
export -f run_query
export DB_MAP
export TMP_DIR
export OUTPUT_FILE
export OUTPUT_FORMAT

# Run all queries in parallel
cat tmp_query_list.txt | xargs -n 1 -P "$THREADS" -I{} bash -c 'run_query "$@"' _ {}

# Cleanup
rm -rf tmp_query_list.txt "$TMP_DIR"
echo "✅ Done! Output saved to $OUTPUT_FILE"