#!/bin/bash

current_time=$(date +%Y%m%d%H%M%S)
header="Name,Description,Severity,Message,Path,Start line,Start column,End line,End column"
echo "$header" > codeql_output-$current_time.csv
cp codeql_output-$current_time.csv codeql_output-$current_time.bkup.csv

# Temp dir for per-query results
mkdir -p tmp_codeql_csvs

export CODEQL_DB_BASE="cqlDB-federated"
export CURRENT_TIME=$current_time

# Build the list of queries and databases
find ~/Git/codeql ~/Git/exiv2 -type f \
  | grep -Ei "java|javascript|typescript|python" \
  | grep -Ei "security|cwe|cve|ghsl|weak|unused|duplicate|null" \
  | grep -Ei "\.qls$|\.ql$" \
  | grep -Eiv "\.qll$|\.qlref$|test/" \
  > tmp_query_list.txt

# Function for parallel execution (exported for xargs)
run_query() {
  local query="$1"
  local lang
  lang=$(echo "$query" | grep -Eio 'java|javascript|typescript|python' | head -n1)
  out_file="tmp_codeql_csvs/$(basename "$query" | sed 's/\.[^.]*$//')_$lang.csv"

  echo "[$lang] Running: $query"
  codeql database analyze "$CODEQL_DB_BASE/$lang" \
    --format=csv \
    --output="$out_file" \
    "$query" 2>/dev/null

  # Append to backup file
  if [ -f "$out_file" ]; then
    tail -n +2 "$out_file" >> codeql_output-$CURRENT_TIME.bkup.csv
  fi
}
export -f run_query

# Run in parallel (adjust -P for number of cores)
cat tmp_query_list.txt | xargs -n 1 -P 6 -I{} bash -c 'run_query "$@"' _ {}

# Clean up temp
rm -rf tmp_codeql_csvs tmp_query_list.txt

# View the final result
vim codeql_output-$current_time.bkup.csv