# Benchmark harness for the three CLI implementations.
#
#   make build    compile all implementations
#   make verify   check that every implementation prints identical bytes
#   make test     run the unit tests of each implementation
#   make bench    measure time, binary size, peak RSS and clean build time
#   make report   turn the measurements into results/report.md
#   make clean    remove build outputs and results (keeps fixtures)
#
# `make bench` depends on `verify`: a benchmark of programs that disagree on
# their output is not a benchmark of the same program.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

ROOT    := $(CURDIR)
BIN     := $(ROOT)/bin
FIX     := $(ROOT)/fixtures
RESULTS := $(ROOT)/results

RS  := $(BIN)/bench-rs
GO  := $(BIN)/bench-go
BUN := $(BIN)/bench-bun
BUNBC := $(BIN)/bench-bun-bytecode

# Benchmark inputs. Override on the command line for a quicker loop, e.g.
#   make bench PRIMES=1000000 WARMUP=2
RECORDS ?= 200000
FILES   ?= 4000
BLOB_MIB ?= 64
PRIMES  ?= 20000000
SEED    ?= 20260918
WARMUP  ?= 3
RUNS    ?= 10

# Startup cases run far more often because each run is sub-millisecond.
STARTUP_RUNS ?= 50

UNAME := $(shell uname -s)

.PHONY: all build verify test bench report clean fixtures tools help

all: build verify test

help:
	@grep -E '^#   make ' $(MAKEFILE_LIST) | sed 's/^#   //'

# ---------------------------------------------------------------- build

build: $(RS) $(GO) $(BUN) $(BUNBC)

$(RS): rust/Cargo.toml rust/src/main.rs
	@mkdir -p $(BIN)
	cd rust && cargo build --release --quiet
	cp rust/target/release/bench-rs $@

$(GO): go.mod go/main.go
	@mkdir -p $(BIN)
	go build -trimpath -ldflags "-s -w" -o $@ ./go

$(BUN): bun/src/main.ts bun/package.json
	@mkdir -p $(BIN)
	cd bun && bun build src/main.ts --compile --minify --outfile $@

# Bun can embed pre-parsed bytecode, which trades a larger binary for a faster
# start. Both variants are measured so the trade-off is visible.
$(BUNBC): bun/src/main.ts bun/package.json
	@mkdir -p $(BIN)
	cd bun && bun build src/main.ts --compile --minify --bytecode --outfile $@

# ---------------------------------------------------------------- fixtures

fixtures: $(FIX)/records.json

$(FIX)/records.json: scripts/gen-fixtures/main.go
	go run ./scripts/gen-fixtures \
		-dir $(FIX) -records $(RECORDS) -files $(FILES) \
		-blob-mib $(BLOB_MIB) -seed $(SEED)

# ---------------------------------------------------------------- verify

# Every implementation must print the same bytes for the same input.
verify: build fixtures
	@echo "==> verify: comparing output of all implementations"
	@fail=0; \
	for case in "json $(FIX)/records.json" "walk $(FIX)/tree" "hash $(FIX)/blob.bin" "primes 1000000"; do \
	  ref=$$($(RS) $$case); \
	  for impl in $(GO) $(BUN) $(BUNBC); do \
	    got=$$($$impl $$case); \
	    if [[ "$$ref" != "$$got" ]]; then \
	      echo "MISMATCH: $$impl $$case"; \
	      diff <(echo "$$ref") <(echo "$$got") | head -5 || true; \
	      fail=1; \
	    fi; \
	  done; \
	  echo "    ok: $$case"; \
	done; \
	if [[ $$fail -ne 0 ]]; then echo "verify failed"; exit 1; fi; \
	echo "==> verify: all implementations agree"

# ---------------------------------------------------------------- test

test:
	@echo "==> test: rust"
	cd rust && cargo test --quiet
	@echo "==> test: go"
	go test ./go/...
	@echo "==> test: bun"
	cd bun && bun test

# ---------------------------------------------------------------- bench

bench: verify
	@mkdir -p $(RESULTS)/hyperfine
	@echo "==> bench: environment"
	@$(MAKE) --no-print-directory _env > $(RESULTS)/env.json
	@echo "==> bench: startup (--version, --help)"
	@for case in version help; do \
	  flag="--$$case"; \
	  hyperfine --warmup $(WARMUP) --runs $(STARTUP_RUNS) --shell=none \
	    --export-json $(RESULTS)/hyperfine/$$case.json \
	    "$(RS) $$flag" "$(GO) $$flag" "$(BUN) $$flag" "$(BUNBC) $$flag" >/dev/null; \
	  echo "    done: $$case"; \
	done
	@echo "==> bench: workloads"
	@hyperfine --warmup $(WARMUP) --runs $(RUNS) --shell=none \
	  --export-json $(RESULTS)/hyperfine/json.json \
	  "$(RS) json $(FIX)/records.json" "$(GO) json $(FIX)/records.json" \
	  "$(BUN) json $(FIX)/records.json" "$(BUNBC) json $(FIX)/records.json" >/dev/null
	@echo "    done: json"
	@hyperfine --warmup $(WARMUP) --runs $(RUNS) --shell=none \
	  --export-json $(RESULTS)/hyperfine/walk.json \
	  "$(RS) walk $(FIX)/tree" "$(GO) walk $(FIX)/tree" \
	  "$(BUN) walk $(FIX)/tree" "$(BUNBC) walk $(FIX)/tree" >/dev/null
	@echo "    done: walk"
	@hyperfine --warmup $(WARMUP) --runs $(RUNS) --shell=none \
	  --export-json $(RESULTS)/hyperfine/hash.json \
	  "$(RS) hash $(FIX)/blob.bin" "$(GO) hash $(FIX)/blob.bin" \
	  "$(BUN) hash $(FIX)/blob.bin" "$(BUNBC) hash $(FIX)/blob.bin" >/dev/null
	@echo "    done: hash"
	@hyperfine --warmup $(WARMUP) --runs $(RUNS) --shell=none \
	  --export-json $(RESULTS)/hyperfine/primes.json \
	  "$(RS) primes $(PRIMES)" "$(GO) primes $(PRIMES)" \
	  "$(BUN) primes $(PRIMES)" "$(BUNBC) primes $(PRIMES)" >/dev/null
	@echo "    done: primes"
	@echo "==> bench: binary sizes"
	@$(MAKE) --no-print-directory _sizes > $(RESULTS)/sizes.json
	@echo "==> bench: peak RSS"
	@$(MAKE) --no-print-directory _rss > $(RESULTS)/rss.json
	@echo "==> bench: clean build time"
	@$(MAKE) --no-print-directory _build_times > $(RESULTS)/build.json
	@echo "==> bench: done (results in $(RESULTS))"

report:
	@mkdir -p $(RESULTS)
	go run ./scripts/report -dir $(RESULTS) > $(RESULTS)/report.md
	@echo "==> report: $(RESULTS)/report.md"
	@cat $(RESULTS)/report.md

# ---------------------------------------------------------------- internals

# Peak resident set size. The flag and the unit differ between platforms:
# macOS `/usr/bin/time -l` prints bytes, GNU `/usr/bin/time -v` prints KiB.
define measure_rss
$(shell \
  if [[ "$(UNAME)" == "Darwin" ]]; then \
    /usr/bin/time -l $(1) >/dev/null 2>$(RESULTS)/.rss.txt || true; \
    awk '/maximum resident set size/ {print $$1}' $(RESULTS)/.rss.txt | head -1; \
  else \
    /usr/bin/time -v $(1) >/dev/null 2>$(RESULTS)/.rss.txt || true; \
    awk -F': *' '/Maximum resident set size/ {print $$2 * 1024}' $(RESULTS)/.rss.txt | head -1; \
  fi)
endef

.PHONY: _env _sizes _rss _build_times

_env:
	@printf '{\n'
	@printf '  "os": "%s",\n' "$$(uname -sr)"
	@printf '  "arch": "%s",\n' "$$(uname -m)"
	@if [[ "$(UNAME)" == "Darwin" ]]; then \
	  printf '  "cpu": "%s",\n' "$$(sysctl -n machdep.cpu.brand_string)"; \
	  printf '  "cores": "%s",\n' "$$(sysctl -n hw.ncpu)"; \
	else \
	  printf '  "cpu": "%s",\n' "$$(awk -F': ' '/model name/ {print $$2; exit}' /proc/cpuinfo)"; \
	  printf '  "cores": "%s",\n' "$$(nproc)"; \
	fi
	@printf '  "runner": "%s",\n' "$${RUNNER_OS:-local}"
	@printf '  "timestamp": "%s",\n' "$$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	@printf '  "workload": {"records": "%s", "files": "%s", "blob_mib": "%s", "primes": "%s", "runs": "%s", "startup_runs": "%s"},\n' \
	  "$(RECORDS)" "$(FILES)" "$(BLOB_MIB)" "$(PRIMES)" "$(RUNS)" "$(STARTUP_RUNS)"
	@printf '  "versions": {\n'
	@printf '    "rustc": "%s",\n' "$$(rustc --version)"
	@printf '    "cargo": "%s",\n' "$$(cargo --version)"
	@printf '    "go": "%s",\n' "$$(go version)"
	@printf '    "bun": "%s",\n' "$$(bun --version)"
	@printf '    "hyperfine": "%s"\n' "$$(hyperfine --version)"
	@printf '  }\n}\n'

_sizes:
	@printf '{\n'
	@printf '  "rust": %s,\n' "$$(wc -c < $(RS) | tr -d ' ')"
	@printf '  "go": %s,\n' "$$(wc -c < $(GO) | tr -d ' ')"
	@printf '  "bun": %s,\n' "$$(wc -c < $(BUN) | tr -d ' ')"
	@printf '  "bun-bytecode": %s\n' "$$(wc -c < $(BUNBC) | tr -d ' ')"
	@printf '}\n'

_rss:
	@mkdir -p $(RESULTS)
	@printf '{\n'
	@first_case=1; \
	for spec in "version:--version" "help:--help" "json:json $(FIX)/records.json" "walk:walk $(FIX)/tree" "hash:hash $(FIX)/blob.bin" "primes:primes $(PRIMES)"; do \
	  name=$${spec%%:*}; args=$${spec#*:}; \
	  if [[ $$first_case -eq 0 ]]; then printf ',\n'; fi; first_case=0; \
	  printf '  "%s": {' "$$name"; \
	  first_impl=1; \
	  for pair in "rust:$(RS)" "go:$(GO)" "bun:$(BUN)" "bun-bytecode:$(BUNBC)"; do \
	    impl=$${pair%%:*}; binary=$${pair#*:}; \
	    if [[ "$(UNAME)" == "Darwin" ]]; then \
	      /usr/bin/time -l $$binary $$args >/dev/null 2>$(RESULTS)/.rss.txt || true; \
	      value=$$(awk '/maximum resident set size/ {print $$1; exit}' $(RESULTS)/.rss.txt); \
	    else \
	      /usr/bin/time -v $$binary $$args >/dev/null 2>$(RESULTS)/.rss.txt || true; \
	      value=$$(awk -F': *' '/Maximum resident set size/ {print $$2 * 1024; exit}' $(RESULTS)/.rss.txt); \
	    fi; \
	    if [[ -z "$$value" ]]; then value=0; fi; \
	    if [[ $$first_impl -eq 0 ]]; then printf ','; fi; first_impl=0; \
	    printf '"%s":%s' "$$impl" "$$value"; \
	  done; \
	  printf '}'; \
	done; \
	printf '\n}\n'
	@rm -f $(RESULTS)/.rss.txt

_build_times:
	@printf '{\n'
	@cd rust && cargo clean --quiet
	@start=$$(date +%s.%N); (cd rust && cargo build --release --quiet); end=$$(date +%s.%N); \
	  printf '  "rust": %s,\n' "$$(echo "$$end $$start" | awk '{printf "%.6f", $$1 - $$2}')"
	@go clean -cache >/dev/null 2>&1 || true
	@start=$$(date +%s.%N); go build -trimpath -ldflags "-s -w" -o /dev/null ./go; end=$$(date +%s.%N); \
	  printf '  "go": %s,\n' "$$(echo "$$end $$start" | awk '{printf "%.6f", $$1 - $$2}')"
	@start=$$(date +%s.%N); (cd bun && bun build src/main.ts --compile --minify --outfile /tmp/bench-bun-buildtime >/dev/null); end=$$(date +%s.%N); \
	  printf '  "bun": %s,\n' "$$(echo "$$end $$start" | awk '{printf "%.6f", $$1 - $$2}')"
	@start=$$(date +%s.%N); (cd bun && bun build src/main.ts --compile --minify --bytecode --outfile /tmp/bench-bun-bytecode-buildtime >/dev/null); end=$$(date +%s.%N); \
	  printf '  "bun-bytecode": %s\n' "$$(echo "$$end $$start" | awk '{printf "%.6f", $$1 - $$2}')"
	@printf '}\n'
	@rm -f /tmp/bench-bun-buildtime /tmp/bench-bun-bytecode-buildtime
	@cp rust/target/release/bench-rs $(RS)

# ---------------------------------------------------------------- clean

clean:
	rm -rf $(BIN) $(RESULTS)
	cd rust && cargo clean --quiet || true
	@echo "==> clean: removed bin/ and results/ (fixtures kept; use 'rm -rf fixtures' to drop them)"
