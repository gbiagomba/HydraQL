# ===== HydraQL Makefile =====

PY            ?= python3
SCRIPT        ?= hydraql.py

DB_ROOT       ?= ./cqlDB
LANGS         ?= "javascript,typescript,python,java"
QUERY_DIRS    ?= "$(HOME)/Git/codeql,$(HOME)/Git/CQL"
FORMAT        ?= csv
THREADS       ?= 8
SEVERITY      ?=
STRICT        ?= 0
FANCY         ?= 1
DRY_RUN       ?= 0
ALLOW_MISSING ?= 0
NO_PACK       ?= 0
VERBOSE       ?= 1
SUITE_ONLY    ?= 0

# New automation flags
UNLOCK_CACHE      ?= 1
CHECK_LOCK_PROC   ?= 1
KILL_LOCK_PROC    ?= 0
AUTO_FINALIZE_DB  ?= 1
AUTO_INIT_DB      ?= 0
SOURCE_ROOT       ?=

# Docker
IMAGE         ?= hydraql:latest
CODEQL_VERSION?= 2.17.6

# Flags
ifeq ($(FANCY),1)
  FANCY_FLAG := --fancy
endif
ifeq ($(DRY_RUN),1)
  DRY_FLAG := --dry-run
endif
ifeq ($(ALLOW_MISSING),1)
  ALLOW_MISSING_FLAG := --allow-missing-db
endif
ifeq ($(NO_PACK),1)
  PACK_FLAG := --no-pack-install
endif
ifeq ($(VERBOSE),1)
  VERBOSE_FLAG := --verbose
endif
ifeq ($(SUITE_ONLY),1)
  SUITE_FLAG := --suite-only
endif
ifeq ($(STRICT),1)
  STRICT_FLAG := --strict-severity
endif
ifneq ($(strip $(SEVERITY)),)
  SEVERITY_FLAG := --severity $(SEVERITY)
endif
ifeq ($(UNLOCK_CACHE),1)
  UNLOCK_FLAG := --unlock-cache
endif
ifeq ($(CHECK_LOCK_PROC),1)
  CHECK_LOCK_FLAG := --check-lock-process
endif
ifeq ($(KILL_LOCK_PROC),1)
  KILL_LOCK_FLAG := --kill-lock-process
endif
ifeq ($(AUTO_FINALIZE_DB),1)
  FINALIZE_FLAG := --auto-finalize-db
endif
ifeq ($(AUTO_INIT_DB),1)
  INIT_FLAG := --auto-init-db
endif
ifneq ($(strip $(SOURCE_ROOT)),)
  SRCROOT_FLAG := --source-root $(SOURCE_ROOT)
endif

.PHONY: help run run-sarif run-json run-high docker-build docker-run docker-shell clean

help:
	@echo "HydraQL Makefile"
	@echo " make run             # run HydraQL locally"
	@echo " make run-sarif       # SARIF output"
	@echo " make run-json        # JSON output"
	@echo " make run-high        # severity=HIGH (loose)"
	@echo " make docker-build    # build Docker image"
	@echo " make docker-run      # run in Docker"
	@echo " make docker-shell    # shell inside container"
	@echo " make clean           # remove local artifacts"

run:
	$(PY) $(SCRIPT) \
	  --db-root $(DB_ROOT) \
	  --langs $(LANGS) \
	  --query-dir $(QUERY_DIRS) \
	  --output-format $(FORMAT) \
	  --parallel $(THREADS) \
	  $(SEVERITY_FLAG) \
	  $(STRICT_FLAG) \
	  $(FANCY_FLAG) \
	  $(DRY_FLAG) \
	  $(ALLOW_MISSING_FLAG) \
	  $(PACK_FLAG) \
	  $(VERBOSE_FLAG) \
	  $(SUITE_FLAG) \
	  $(UNLOCK_FLAG) \
	  $(CHECK_LOCK_FLAG) \
	  $(KILL_LOCK_FLAG) \
	  $(FINALIZE_FLAG) \
	  $(INIT_FLAG) \
	  $(SRCROOT_FLAG)

run-sarif:
	$(MAKE) run FORMAT=sarif

run-json:
	$(MAKE) run FORMAT=json

run-high:
	$(MAKE) run SEVERITY=HIGH

docker-build:
	docker build --build-arg CODEQL_VERSION=$(CODEQL_VERSION) -t $(IMAGE) .

docker-run:
	@Q1=$$(echo $(QUERY_DIRS) | cut -d, -f1); \
	 Q2=$$(echo $(QUERY_DIRS) | cut -d, -f2); \
	 docker run --rm \
	  -v "$$(pwd)/$(DB_ROOT):/work/cqlDB" \
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
	  $(STRICT_FLAG) \
	  $(FANCY_FLAG) \
	  $(DRY_FLAG) \
	  $(ALLOW_MISSING_FLAG) \
	  $(PACK_FLAG) \
	  $(VERBOSE_FLAG) \
	  $(SUITE_FLAG) \
	  $(UNLOCK_FLAG) \
	  $(CHECK_LOCK_FLAG) \
	  $(KILL_LOCK_FLAG) \
	  $(FINALIZE_FLAG) \
	  $(INIT_FLAG) \
	  $(SRCROOT_FLAG)

docker-shell:
	docker run --rm -it \
	  -v "$$(pwd)/$(DB_ROOT):/work/cqlDB" \
	  -w /work \
	  $(IMAGE) /bin/sh

clean:
	rm -rf tmp_hydraql_output \
	  HydraQL_output-*.csv \
	  HydraQL_output-*.json \
	  HydraQL_output-*.sarif \
	  hydraql_failures.log