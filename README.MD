# HydraQL

## Background / Lore
**HydraQL** is a parallel CodeQL scanning engine — many-headed like the Hydra 🐍.  
It runs CodeQL queries against multiple databases, in parallel, across multiple languages, and merges results into CSV, JSON, or SARIF outputs.  

Born from the need to scale CodeQL analysis across large, multi-language codebases, HydraQL aims to be fast, flexible, and mythic.

---

## Table of Contents
- [Features](#features)
- [Installation](#installation)
  - [Using Docker](#using-docker)
  - [From Source](#from-source)
- [Usage](#usage)
  - [Flags](#flags)
  - [Examples](#examples)
- [Contributing](#contributing)
- [License](#license)

---

## Features
- 🔥 Parallel query execution (`--parallel 8`)
- 📦 Auto `codeql pack install`
- 🧩 Multiple query directories (comma-separated or repeated)
- 🐍 Language aliasing (TypeScript→JavaScript, Kotlin→Java)
- 🛡️ Database preflight: ensures DB exists & finalized before scanning
- 🎨 Fancy colored output (`--fancy`)
- 🔍 Severity filtering (`--severity HIGH`)
- 📊 ASCII summary chart
- 📂 Outputs in CSV, JSON, or SARIF

---

## Installation

### Using Docker
```bash
# Build
docker build -t hydraql:latest .

# Run
docker run --rm \
  -v "$PWD/cqlDB:/work/cqlDB:ro" \
  -v "$HOME/Git/codeql:/work/queries:ro" \
  hydraql:latest \
  --db-root /work/cqlDB \
  --langs "javascript,typescript,python,java" \
  --query-dir "/work/queries" \
  --output-format sarif \
  --fancy
```

## From Source
```bash
# Prerequisites: Python 3.7+, CodeQL CLI in PATH
git clone https://github.com/yourorg/hydraql.git
cd hydraql
python3 hydraql.py --help
```

---

# Usage

## Flags
```
--dir <path>            (reserved) source directory
--langs <list>          Comma-separated list of languages
--db-root <path>        Root directory of CodeQL databases
--query-dir <path>      Query directories (repeatable or comma-separated)
--threads / --parallel  Number of parallel jobs
--output-format         csv, json, or sarif (default: csv)
--severity <level>      Filter results by severity (HIGH, CRITICAL, etc.)
--fancy                 Enable fancy colored output
--dry-run               Show what would run without executing
--pack-install          Auto-run `codeql pack install`
--allow-missing-db      Continue even if some DBs are missing/unfinalized
```

## Examples
```bash
# Basic usage
# Run queries in parallel with fancy mode
python hydraql.py \
  --db-root ./cqlDB \
  --langs "javascript,typescript,python" \
  --query-dir "~/Git/codeql,~/Git/CQL" \
  --parallel 8 \
  --output-format csv \
  --fancy

# Run with severity filtering
python hydraql.py \
  --db-root ./cqlDB \
  --langs "java" \
  --query-dir "~/Git/codeql" \
  --severity HIGH
```

---

## Contributing

PRs are welcome! Ideas include:
	•	More language aliases
	•	Plugin system for custom report exporters
	•	GitHub Actions integration with SARIF uploads

---

## License

This project is licensed under the GPL-3.0 License.
See the LICENSE file for details.
