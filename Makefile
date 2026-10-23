# ===== HydraQL Makefile =====
VERSION       ?= 2.7.0

# ---- Go ----
GO            ?= go
GO_CMD        := ./cmd/hydraql
BIN           ?= ./hydraql
LDFLAGS       := -ldflags="-s -w -X main.version=$(VERSION)"
DIST          := dist

# ---- Python (legacy) ----
PY            ?= python3
SCRIPT        ?= legacy/hydraql.py

# ---- Scan settings ----
DB_ROOT       ?= ./cqlDB
LANGS         ?= javascript,typescript,python,java
QUERY_DIRS    ?= $(HOME)/Git/codeql
FORMAT        ?= csv
THREADS       ?= 8
SEVERITY      ?=
STRICT        ?= 0
FANCY         ?= 1
DRY_RUN       ?= 0
ALLOW_MISSING ?= 0
PACK_INSTALL  ?= 0
VERBOSE       ?= 1
SUITE_ONLY    ?= 0

# ---- Lock and DB automation ----
UNLOCK_CACHE      ?= 1
CHECK_LOCK_PROC   ?= 1
KILL_LOCK_PROC    ?= 0
AUTO_FINALIZE_DB  ?= 1
AUTO_INIT_DB      ?= 0
SOURCE_ROOT       ?=
FORCE_UNREADY     ?= 0
QUERY_TIMEOUT     ?= 600
NO_TIMEOUT        ?= 0

# ---- Database build phase (Go only) ----
BUILD_ROOT        ?=
SOURCE_DIRS       ?=
DB_PREFIX         ?=
EXCLUDE           ?=
REBUILD_DB        ?= 0
RESOLVE_SYMLINKS  ?= 1
BUILD_TIMEOUT     ?= 0
BUILD_THREADS     ?= 1
BUILD_COMMAND     ?=
BUILD_MODE        ?=

# ---- Docker ----
IMAGE         ?= hydraql:latest
CODEQL_VERSION?= 2.25.6

# ============================================================
# Flag assembly
# ============================================================
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
  SEVERITY_FLAG := --severity "$(SEVERITY)"
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
  SRCROOT_FLAG := --source-root "$(SOURCE_ROOT)"
endif
ifeq ($(FORCE_UNREADY),1)
  FORCE_UNREADY_FLAG := --force-scan-unready
endif
ifneq ($(QUERY_TIMEOUT),)
  TIMEOUT_FLAG := --query-timeout $(QUERY_TIMEOUT)
endif
ifeq ($(NO_TIMEOUT),1)
  TIMEOUT_FLAG := --no-timeout
endif

# Build-phase flags, understood only by the Go binary.
ifneq ($(strip $(BUILD_ROOT)),)
  BUILD_ROOT_FLAG := --build-root "$(BUILD_ROOT)"
endif
ifneq ($(strip $(SOURCE_DIRS)),)
  SOURCE_DIRS_FLAG := --source-dir "$(SOURCE_DIRS)"
endif
ifneq ($(strip $(DB_PREFIX)),)
  DB_PREFIX_FLAG := --db-prefix "$(DB_PREFIX)"
endif
ifneq ($(strip $(EXCLUDE)),)
  EXCLUDE_FLAG := --exclude "$(EXCLUDE)"
endif
ifeq ($(REBUILD_DB),1)
  REBUILD_FLAG := --rebuild-db
endif
ifeq ($(RESOLVE_SYMLINKS),0)
  NO_RESOLVE_FLAG := --no-resolve-symlinks
endif
ifneq ($(strip $(BUILD_COMMAND)),)
  BUILD_COMMAND_FLAG := --build-command "$(BUILD_COMMAND)"
endif
ifneq ($(strip $(BUILD_MODE)),)
  BUILD_MODE_FLAG := --build-mode "$(BUILD_MODE)"
endif

COMMON_FLAGS := \
  --db-root "$(DB_ROOT)" \
  --langs "$(LANGS)" \
  --query-dir "$(QUERY_DIRS)" \
  --output-format $(FORMAT) \
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
  $(FORCE_UNREADY_FLAG) \
  $(TIMEOUT_FLAG)

BUILD_FLAGS := \
  $(BUILD_ROOT_FLAG) \
  $(SOURCE_DIRS_FLAG) \
  $(DB_PREFIX_FLAG) \
  $(EXCLUDE_FLAG) \
  $(REBUILD_FLAG) \
  $(NO_RESOLVE_FLAG) \
  $(BUILD_COMMAND_FLAG) \
  $(BUILD_MODE_FLAG) \
  --build-timeout $(BUILD_TIMEOUT) \
  --build-threads $(BUILD_THREADS)

.PHONY: help \
        build build-all go-test go-vet go-fmt go-clean \
        scan scan-sarif scan-json scan-high all build-db \
        run run-sarif run-json run-high \
        docker-build docker-run docker-shell clean

help:
	@echo "HydraQL Makefile (v$(VERSION))"
	@echo ""
	@echo "=== Build the binary ==="
	@echo " make build                      # native binary -> $(BIN)"
	@echo " make build-all                  # cross-compile all 6 targets -> $(DIST)/"
	@echo " make go-test                    # run Go tests"
	@echo " make go-vet                     # run go vet"
	@echo " make go-fmt                     # report unformatted files"
	@echo " make go-clean                   # remove $(DIST)/ and $(BIN)"
	@echo ""
	@echo "=== Run (Go) ==="
	@echo " make scan                       # scan existing databases under DB_ROOT"
	@echo " make scan-sarif                 # SARIF output"
	@echo " make scan-json                  # JSON output"
	@echo " make scan-high                  # severity=HIGH (loose match)"
	@echo " make build-db BUILD_ROOT=<dir>  # create+finalize a DB per project, no scan"
	@echo " make all BUILD_ROOT=<dir>       # create+finalize, then scan everything"
	@echo ""
	@echo "   One-stop example:"
	@echo "     make all BUILD_ROOT=~/Projects/App/src DB_ROOT=./cqlDB \\"
	@echo "       LANGS=javascript,typescript FORMAT=sarif"
	@echo ""
	@echo "   Source roots are resolved through their symlinks by default."
	@echo "   Set RESOLVE_SYMLINKS=0 to disable, REBUILD_DB=1 to force --overwrite."
	@echo ""
	@echo "   Compiled languages need a build command:"
	@echo "     make all BUILD_ROOT=. DB_ROOT=./cqlDB LANGS=java,cpp \\"
	@echo "       BUILD_COMMAND=./build.sh"
	@echo ""
	@echo "=== Run (Python legacy, v1.8.0 feature level) ==="
	@echo " make run                        # run legacy/hydraql.py"
	@echo " make run-sarif / run-json / run-high"
	@echo "   Note: the legacy script still has the --format sarif/json defect"
	@echo "   fixed in the Go edition, so only FORMAT=csv yields results there."
	@echo ""
	@echo "=== Docker ==="
	@echo " make docker-build               # build Docker image"
	@echo " make docker-run                 # run in Docker"
	@echo " make docker-shell               # shell inside container"
	@echo ""
	@echo " make clean                      # remove build and run artifacts"

# ============================================================
# Go build targets
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

go-fmt:
	@out=$$(gofmt -l .); \
	 if [ -n "$$out" ]; then echo "Unformatted files:"; echo "$$out"; exit 1; fi; \
	 echo "gofmt clean"

go-clean:
	rm -rf $(DIST) hydraql

# ============================================================
# Go run targets
# ============================================================
scan: build
	$(BIN) $(COMMON_FLAGS) --threads $(THREADS)

scan-sarif:
	$(MAKE) scan FORMAT=sarif

scan-json:
	$(MAKE) scan FORMAT=json

scan-high:
	$(MAKE) scan SEVERITY=HIGH

# Build a database cluster per project source dir, then scan them all.
all: build
	@if [ -z "$(strip $(BUILD_ROOT))$(strip $(SOURCE_DIRS))" ]; then \
	  echo "Set BUILD_ROOT=<dir> or SOURCE_DIRS=<dir[,dir]> for this target."; exit 1; \
	fi
	$(BIN) $(BUILD_FLAGS) $(COMMON_FLAGS) --threads $(THREADS)

# Same build phase, stopping before analysis.
build-db: build
	@if [ -z "$(strip $(BUILD_ROOT))$(strip $(SOURCE_DIRS))" ]; then \
	  echo "Set BUILD_ROOT=<dir> or SOURCE_DIRS=<dir[,dir]> for this target."; exit 1; \
	fi
	$(BIN) $(BUILD_FLAGS) --build-only \
	  --db-root "$(DB_ROOT)" \
	  --langs "$(LANGS)" \
	  $(FANCY_FLAG) $(DRY_FLAG) $(VERBOSE_FLAG) \
	  $(UNLOCK_FLAG) $(CHECK_LOCK_FLAG) $(KILL_LOCK_FLAG)

# ============================================================
# Python legacy targets
# ============================================================
run:
	$(PY) $(SCRIPT) $(COMMON_FLAGS) --parallel $(THREADS)

run-sarif:
	$(MAKE) run FORMAT=sarif

run-json:
	$(MAKE) run FORMAT=json

run-high:
	$(MAKE) run SEVERITY=HIGH

# ============================================================
# Docker
# ============================================================
docker-build:
	docker build --build-arg CODEQL_VERSION=$(CODEQL_VERSION) -t $(IMAGE) .

docker-run:
	@Q1=$$(echo "$(QUERY_DIRS)" | cut -d, -f1); \
	 Q2=$$(echo "$(QUERY_DIRS)" | cut -d, -f2); \
	 docker run --rm \
	  -v "$$(pwd)/$(DB_ROOT):/work/cqlDB" \
	  $$( [ -n "$$Q1" ] && echo -v "$$Q1:/work/queries1:ro" ) \
	  $$( [ -n "$$Q2" ] && [ "$$Q2" != "$$Q1" ] && echo -v "$$Q2:/work/queries2:ro" ) \
	  -w /work \
	  $(IMAGE) \
	  --db-root /work/cqlDB \
	  --langs "$(LANGS)" \
	  $$( [ -n "$$Q1" ] && echo --query-dir /work/queries1 ) \
	  $$( [ -n "$$Q2" ] && [ "$$Q2" != "$$Q1" ] && echo --query-dir /work/queries2 ) \
	  --output-format $(FORMAT) \
	  --threads $(THREADS) \
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
	  $(FORCE_UNREADY_FLAG) \
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
