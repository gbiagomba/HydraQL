# ===== HydraQL Makefile =====

# --- Config (override on the command line or in your shell env) ---
PY            ?= python3
SCRIPT        ?= hydraql.py

# HydraQL runtime defaults (override per run)
DB_ROOT       ?= ./cqlDB
LANGS         ?= "javascript,typescript,python,java"
QUERY_DIRS    ?= "$(HOME)/Git/codeql,$(HOME)/Git/CQL"
FORMAT        ?= csv
THREADS       ?= 8
SEVERITY      ?=
FANCY         ?= 1
DRY_RUN       ?= 0
ALLOW_MISSING ?= 0
PACK_INSTALL  ?= 0

# Docker bits
IMAGE         ?= hydraql:latest
CODEQL_VERSION?= 2.17.6                                # override if you need a different CLI ver

# Handy: pass extra flags at run time
ARGS          ?=

# Internal flag helpers
ifeq ($(FANCY),1)
  FANCY_FLAG := --fancy
endif
ifeq ($(DRY_RUN),1)
  DRY_FLAG := --dry-run
endif
ifeq ($(ALLOW_MISSING),1)
  ALLOW_MISSING_FLAG := --allow-missing-db
endif
ifeq ($(PACK_INSTALL),1)
  PACK_FLAG := --pack-install
endif
ifneq ($(strip $(SEVERITY)),)
  SEVERITY_FLAG := --severity $(SEVERITY)
endif

# ===== Targets =====
.PHONY: help run run-sarif run-json run-high docker-build docker-run docker-shell clean

help:
	@echo "HydraQL Makefile targets"
	@echo "  make run                 # run HydraQL locally"
	@echo "  make run-sarif           # run HydraQL with SARIF output"
	@echo "  make run-json            # run HydraQL with JSON output"
	@echo "  make run-high            # run HydraQL with severity=HIGH"
	@echo "  make docker-build        # build Docker image ($(IMAGE))"
	@echo "  make docker-run          # run inside Docker (mounts DB_ROOT and QUERY_DIRS)"
	@echo "  make docker-shell        # open a shell in the container"
	@echo "  make clean               # remove local temp outputs"
	@echo ""
	@echo "Override defaults, e.g.:"
	@echo "  make run DB_ROOT=./cqlDB-GrowthSDK LANGS=\"python,cpp,javascript\" THREADS=12"
	@echo "  make docker-build CODEQL_VERSION=2.18.0"
	@echo "  make docker-run QUERY_DIRS=\"/work/queries/codeql,/work/queries/custom\""

# ----- Local Runs -----
run:
	$(PY) $(SCRIPT) \
	  --db-root $(DB_ROOT) \
	  --langs $(LANGS) \
	  --query-dir $(QUERY_DIRS) \
	  --output-format $(FORMAT) \
	  --parallel $(THREADS) \
	  $(SEVERITY_FLAG) \
	  $(FANCY_FLAG) \
	  $(DRY_FLAG) \
	  $(ALLOW_MISSING_FLAG) \
	  $(PACK_FLAG) \
	  $(ARGS)

run-sarif:
	$(MAKE) run FORMAT=sarif

run-json:
	$(MAKE) run FORMAT=json

run-high:
	$(MAKE) run SEVERITY=HIGH

# ----- Docker -----
docker-build:
	docker build --build-arg CODEQL_VERSION=$(CODEQL_VERSION) -t $(IMAGE) .

# Splitting QUERY_DIRS by comma for two common mounts.
# Adjust/add more -v lines or switch to a wrapper if you have >2 dirs.
docker-run:
	@Q1=$$(echo $(QUERY_DIRS) | cut -d, -f1); \
	 Q2=$$(echo $(QUERY_DIRS) | cut -d, -f2); \
	 echo "Mounting QUERY_DIRS: $$Q1 $$Q2"; \
	 docker run --rm \
	  -v "$$(pwd)/$(DB_ROOT):/work/cqlDB:ro" \
	  $$( [ -n "$$Q1" ] && echo -v "$$Q1:/work/queries1:ro" ) \
	  $$( [ -n "$$Q2" ] && echo -v "$$Q2:/work/queries2:ro" ) \
	  -w /work \
	  $(IMAGE) \
	  --db-root /work/cqlDB \
	  --langs $(LANGS) \
	  $$( [ -n "$$Q1" ] && echo --query-dir /work/queries1 ) \
	  $$( [ -n "$$Q2" ] && echo --query-dir /work/queries2 ) \
	  --output-format $(FORMAT) \
	  --parallel $(THREADS) \
	  $(SEVERITY_FLAG) \
	  $(FANCY_FLAG) \
	  $(DRY_FLAG) \
	  $(ALLOW_MISSING_FLAG) \
	  $(PACK_FLAG) \
	  $(ARGS)

docker-shell:
	docker run --rm -it \
	  -v "$$(pwd)/$(DB_ROOT):/work/cqlDB:ro" \
	  -w /work \
	  $(IMAGE) /bin/sh

# ----- Cleanup -----
clean:
	@echo "Cleaning local HydraQL artifacts…"
	rm -rf tmp_hydraql_output \
		HydraQL_output-*.csv \
		HydraQL_output-*.json \
		HydraQL_output-*.sarif