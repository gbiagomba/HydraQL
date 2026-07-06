# ===== HydraQL Makefile =====
VERSION       ?= 2.1.3

# ---- Go ----
GO            ?= go
GO_CMD        := ./cmd/hydraql
LDFLAGS       := -ldflags="-s -w -X main.version=$(VERSION)"
DIST          := dist

# ---- Python (legacy) ----
PY            ?= python3
SCRIPT        ?= legacy/hydraql.py

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
QUERY_TIMEOUT     ?= 600
NO_TIMEOUT        ?= 0

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
ifneq ($(QUERY_TIMEOUT),)
  TIMEOUT_FLAG := --query-timeout $(QUERY_TIMEOUT)
endif
ifeq ($(NO_TIMEOUT),1)
  TIMEOUT_FLAG := --no-timeout
endif

.PHONY: help \
        build build-all go-test go-vet go-clean \
        run run-sarif run-json run-high \
        docker-build docker-run docker-shell clean

help:
	@echo "HydraQL Makefile (v$(VERSION))"
	@echo ""
	@echo "=== Go (recommended) ==="
	@echo " make build                      # build native binary → ./hydraql"
	@echo " make build-all                  # cross-compile all 6 targets → dist/"
	@echo " make go-test                    # run Go tests"
	@echo " make go-vet                     # run go vet"
	@echo " make go-clean                   # remove dist/ and ./hydraql"
	@echo ""
	@echo "=== Python (legacy) ==="
	@echo " make run                        # run Python HydraQL locally"
	@echo " make run-sarif                  # SARIF output"
	@echo " make run-json                   # JSON output"
	@echo " make run-high                   # severity=HIGH (loose)"
	@echo " make run QUERY_TIMEOUT=1800     # 30-min per-query timeout"
	@echo " make run NO_TIMEOUT=1           # disable timeout"
	@echo ""
	@echo "=== Docker ==="
	@echo " make docker-build               # build Docker image"
	@echo " make docker-run                 # run in Docker"
	@echo " make docker-shell               # shell inside container"
	@echo ""
	@echo " make clean                      # remove all artifacts"

# ============================================================
# Go targets
# ============================================================
build:
	$(GO) build $(LDFLAGS) -o hydraql $(GO_CMD)

build-all: $(DIST)
	GOOS=darwin  GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(DIST)/hydraql-darwin-amd64   $(GO_CMD)
	GOOS=darwin  GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(DIST)/hydraql-darwin-arm64   $(GO_CMD)
	GOOS=linux   GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(DIST)/hydraql-linux-amd64    $(GO_CMD)
	GOOS=linux   GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(DIST)/hydraql-linux-arm64    $(GO_CMD)
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(DIST)/hydraql-windows-amd64.exe $(GO_CMD)
	GOOS=windows GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(DIST)/hydraql-windows-arm64.exe $(GO_CMD)
	@echo "Built all targets:"
	@ls -lh $(DIST)/

$(DIST):
	mkdir -p $(DIST)

go-test:
	$(GO) test ./...

go-vet:
	$(GO) vet ./...

go-clean:
	rm -rf $(DIST) hydraql

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
	  $(SRCROOT_FLAG) \
	  $(TIMEOUT_FLAG)

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
	  $(SRCROOT_FLAG) \
	  $(TIMEOUT_FLAG)

docker-shell:
	docker run --rm -it \
	  -v "$$(pwd)/$(DB_ROOT):/work/cqlDB" \
	  -w /work \
	  $(IMAGE) /bin/sh

clean: go-clean
	rm -rf tmp_hydraql_output \
	  HydraQL_output-*.csv \
	  HydraQL_output-*.json \
	  HydraQL_output-*.sarif \
	  hydraql_failures.log