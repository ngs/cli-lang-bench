# cli-lang-bench

Startup and throughput benchmarks for small CLI tools written in Rust, Go, and Bun + TypeScript.

The same command line tool is implemented three times. The implementations must
produce **byte-identical output**, which `make verify` checks before any
benchmark is trusted. Then `make bench` measures wall clock time, binary size,
peak memory and clean build time, and `make report` turns the raw numbers into
a table.

## Why this exists

Picking a language for a small CLI usually comes down to a few practical
questions: how long does it take to start, how fast is it on real work, how big
is the binary you have to ship, and how long do you wait for a build. Most
published comparisons answer one of those and quietly change the program
between languages. This repository answers all four and refuses to run unless
the three programs agree on their output down to the byte.

## What is measured

| Case | What it exercises |
|---|---|
| `--version`, `--help` | Process startup only. No file I/O, no allocation beyond the runtime's own. |
| `json <file>` | Parse a ~12 MB JSON array of 200,000 records, group by a field, aggregate. |
| `walk <dir>` | Recursive directory traversal of ~4,000 files, counting files and bytes per extension. |
| `hash <file>` | SHA-256 of a 64 MiB file. Streamed in 1 MiB chunks, so it is CPU and I/O together. |
| `primes <n>` | Sieve of Eratosthenes up to 20,000,000. The only case with no I/O. |

Alongside the timings: **binary size**, **peak resident set size** per case, and
**clean build time**.

## What is not measured

- **Long-running or concurrent workloads.** Everything here is a one-shot CLI
  invocation. Nothing measures throughput under load, goroutine or async
  scheduling, or garbage collection under sustained pressure.
- **Compile-time-heavy Rust patterns.** The Rust implementation is deliberately
  plain. Generic-heavy code would change the build time numbers considerably.
- **Cold page cache.** The fixtures are read repeatedly, so the kernel has them
  cached. This measures the code, not the disk.
- **Distribution size after compression.** Binary size is the size on disk, not
  what a release archive would be.
- **Memory over time.** Peak RSS is a single number from `/usr/bin/time`; it
  says nothing about allocation churn.

## Fairness notes

Benchmarks between languages are easy to get wrong. The choices here, and why:

- **Argument parsing is hand written in all three.** Rust's `clap` is excellent
  but it has real startup and binary size cost, and Go and Bun would be compared
  against it with their standard libraries. Since `--version` is a startup
  measurement, a dependency on one side only would measure the library rather
  than the language. All three parse their own arguments in the same handful of
  lines.
- **JSON and SHA-256 come from each ecosystem's standard answer.** Go and Bun
  have both in their standard library. Rust has neither, so it uses `serde_json`
  and `sha2`, which is what a Rust author would actually write. Hand-rolling
  either in Rust would measure the author, not the language.
- **`sha2` is built with its `asm` feature.** This one matters. Go's
  `crypto/sha256` uses the ARMv8 SHA extensions automatically; the pure-Rust
  `sha2` does not. Measured on an M4 Max, the default build took **123 ms**
  against Go's **37 ms**, and enabling `asm` brought it to **35 ms**. The
  original number was not "Rust is slow at hashing", it was "one side was using
  hardware acceleration and the other was not". If you fork this, check for that
  class of mistake before publishing anything.
- **Release flags are the ones you would ship.** Rust: `opt-level = 3`, LTO,
  `codegen-units = 1`, `panic = "abort"`, stripped. Go: `-trimpath -ldflags
  "-s -w"`. Bun: `--compile --minify`, plus a separate `--bytecode` target so
  the trade-off between binary size and startup is visible.
- **The same algorithm everywhere.** All three use the same odds-only sieve, the
  same 1 MiB hashing chunk, the same explicit stack for directory traversal, and
  the same integer-only rounding for the mean. Output is compact JSON with a
  fixed key order so "identical bytes" is easy to guarantee.
- **Bun is measured as a compiled binary**, not as `bun run script.ts`. That is
  the fair comparison against two compiled languages, and it is how you would
  ship a Bun CLI.

## Results

Numbers move between machines, so the table below is a single measured run on
one machine rather than a claim about the languages in general. Reproduce it
with `make bench report`; CI publishes its own run for `ubuntu-latest` and
`macos-latest` in the job summary of every push.

Measured on an Apple M4 Max (16 cores), macOS 26.6.2 (Darwin 25.6.0, arm64),
with rustc 1.98.1, Go 1.26.4, Bun 1.4.2, hyperfine 1.20.0.

### Time (mean ± σ, ratio against the fastest in the row)

| Case | Rust | Go | Bun | Bun (bytecode) |
|---|---|---|---|---|
| `--version` (startup) | 3.7 ms ± 652 µs (1.00x) | 9.0 ms ± 3.6 ms (2.40x) | 15.5 ms ± 1.6 ms (4.14x) | 11.2 ms ± 992 µs (2.99x) |
| `--help` (startup) | 6.2 ms ± 1.9 ms (1.00x) | 10.0 ms ± 3.0 ms (1.60x) | 15.3 ms ± 2.1 ms (2.47x) | 11.1 ms ± 1.3 ms (1.78x) |
| `json` (parse + aggregate) | 35.9 ms ± 5.0 ms (1.07x) | 109.3 ms ± 3.1 ms (3.25x) | 33.6 ms ± 1.1 ms (1.00x) | 34.9 ms ± 1.8 ms (1.04x) |
| `walk` (traversal) | 22.7 ms ± 1.1 ms (1.00x) | 26.7 ms ± 1.6 ms (1.18x) | 33.5 ms ± 987 µs (1.47x) | 30.5 ms ± 1.1 ms (1.34x) |
| `hash` (SHA-256, 64 MiB) | 40.0 ms ± 1.4 ms (1.11x) | 36.2 ms ± 3.6 ms (1.00x) | 40.6 ms ± 3.2 ms (1.12x) | 39.9 ms ± 1.6 ms (1.10x) |
| `primes` (CPU only) | 32.0 ms ± 1.6 ms (1.02x) | 31.3 ms ± 979 µs (1.00x) | 45.1 ms ± 2.0 ms (1.44x) | 41.1 ms ± 2.6 ms (1.31x) |

### Binary size and clean build

| Implementation | Binary | Clean build |
|---|---|---|
| Rust | 392.7 KiB | 5.75 s |
| Go | 2.1 MiB | 1.85 s |
| Bun | 59.3 MiB | 92.9 ms |
| Bun (bytecode) | 60.8 MiB | 115.8 ms |

Bun's binary embeds the whole runtime, which is why it is two orders of
magnitude larger. Its "build" is a bundling step, not a compile, which is why it
is two orders of magnitude faster.

### Peak memory (max RSS)

| Case | Rust | Go | Bun | Bun (bytecode) |
|---|---|---|---|---|
| `--version` | 1.5 MiB | 4.3 MiB | 14.7 MiB | 15.3 MiB |
| `json` | 27.3 MiB | 56.4 MiB | 51.8 MiB | 52.3 MiB |
| `walk` | 1.8 MiB | 7.8 MiB | 21.4 MiB | 21.8 MiB |
| `hash` | 2.6 MiB | 4.4 MiB | 47.0 MiB | 49.5 MiB |
| `primes` | 11.1 MiB | 14.7 MiB | 32.6 MiB | 32.7 MiB |

### Reading the numbers

- **Startup** is where the three differ most, and it is the number that matters
  for a CLI you invoke in a shell loop. Rust starts in single-digit
  milliseconds; Bun's bytecode build recovers a third of its startup cost over
  the plain compiled build.
- **`json` is not the result most people expect.** Bun's `JSON.parse` is a
  heavily optimised native routine and it beats both compiled languages here.
  Go's `encoding/json` is reflection-based and pays for it.
- **`hash` and `primes` land within noise of each other** across Rust, Go and
  Bun's bytecode build, because all three end up in the same hardware paths.
- **The σ column matters.** Where two means are within one standard deviation of
  each other, the ordering is not meaningful.

## Running it locally

Prerequisites on macOS:

```sh
brew install hyperfine jq
# Rust via rustup, Go and Bun however you normally install them
```

Versions are pinned in [`.tool-versions`](.tool-versions) and in the CI
workflow. Keep the two in sync.

```sh
make build     # compile all four binaries into bin/
make verify    # fail unless every implementation prints identical bytes
make test      # unit tests for each implementation
make bench     # measure (generates fixtures on first run; takes a few minutes)
make report    # write and print results/report.md
make clean     # remove bin/ and results/ (fixtures are kept)
```

`make bench` accepts overrides for a quicker loop:

```sh
make bench RECORDS=20000 FILES=500 BLOB_MIB=8 PRIMES=1000000 RUNS=3 STARTUP_RUNS=10
```

Fixtures are generated from a fixed seed by `scripts/gen-fixtures`, so every
machine measures the same input. They are gitignored rather than committed: the
blob alone is 64 MiB.

## Reading the CI results

Every push to `main`, every pull request and every manual run measures on both
`ubuntu-latest` and `macos-latest`.

- The **job summary** of each run carries the full report.
- The raw JSON and the Markdown are attached as the **`results-<os>` artifact**,
  kept for 30 days.

**Shared runners are noisy.** GitHub-hosted machines vary in CPU model between
runs, are shared with other tenants, and scale frequency unpredictably. CI runs
smaller workloads than the local defaults for that reason. Treat CI numbers as a
regression signal — "did something get dramatically slower" — and not as a
measurement you can quote. For numbers worth comparing, run it locally on an
idle machine.

Third-party actions are pinned to commit SHAs rather than tags, with the release
noted in a comment. A tag can be moved to new code after review; a SHA cannot.

## Layout

```
rust/                 Rust implementation (bench-rs)
go/                   Go implementation (bench-go)
bun/                  Bun + TypeScript implementation (bench-bun)
scripts/gen-fixtures  Deterministic test data generator (Go)
scripts/report        Measurements to Markdown (Go)
Makefile              The harness
```

The report generator is written in Go rather than a scripting language so the
repository needs no interpreter beyond the three already under test.

## License

MIT. See [LICENSE](LICENSE).
