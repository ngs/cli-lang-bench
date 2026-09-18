// Command report turns the raw measurements into a Markdown report.
//
// Inputs (all produced by `make bench`):
//
//	results/hyperfine/<case>.json  timing, one file per benchmark case
//	results/sizes.json             binary sizes
//	results/rss.json               peak resident set size per case
//	results/build.json             clean build times
//	results/env.json               OS, CPU, tool versions, timestamp
//
// Output is Markdown on stdout, which `make report` writes to
// results/report.md and CI also appends to the job summary.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// hyperfineFile is the subset of hyperfine's --export-json we rely on.
type hyperfineFile struct {
	Results []struct {
		Command string  `json:"command"`
		Mean    float64 `json:"mean"`
		Stddev  float64 `json:"stddev"`
		Median  float64 `json:"median"`
		Min     float64 `json:"min"`
		Max     float64 `json:"max"`
	} `json:"results"`
}

type env struct {
	OS        string            `json:"os"`
	Arch      string            `json:"arch"`
	CPU       string            `json:"cpu"`
	Cores     string            `json:"cores"`
	Timestamp string            `json:"timestamp"`
	Runner    string            `json:"runner"`
	Versions  map[string]string `json:"versions"`
}

// implementations in the order they should appear in every table.
var impls = []string{"rust", "go", "bun", "bun-bytecode"}

var implLabels = map[string]string{
	"rust":         "Rust",
	"go":           "Go",
	"bun":          "Bun",
	"bun-bytecode": "Bun (bytecode)",
}

// cases in the order they should appear.
var cases = []string{"version", "help", "json", "walk", "hash", "primes"}

var caseLabels = map[string]string{
	"version": "`--version` (startup)",
	"help":    "`--help` (startup)",
	"json":    "`json` (parse + aggregate)",
	"walk":    "`walk` (directory traversal)",
	"hash":    "`hash` (SHA-256, 64 MiB)",
	"primes":  "`primes` (CPU only)",
}

func main() {
	dir := flag.String("dir", "results", "directory holding the measurements")
	flag.Parse()

	out, err := build(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "report: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(out)
}

func build(dir string) (string, error) {
	var b strings.Builder

	b.WriteString("# Benchmark results\n\n")

	e, err := readEnv(filepath.Join(dir, "env.json"))
	if err == nil {
		b.WriteString("## Environment\n\n")
		b.WriteString("| Item | Value |\n|---|---|\n")
		row(&b, "OS", e.OS)
		row(&b, "Arch", e.Arch)
		row(&b, "CPU", e.CPU)
		row(&b, "Cores", e.Cores)
		if e.Runner != "" {
			row(&b, "Runner", e.Runner)
		}
		row(&b, "Measured at", e.Timestamp)
		keys := make([]string, 0, len(e.Versions))
		for k := range e.Versions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			row(&b, k, e.Versions[k])
		}
		b.WriteString("\n")
	}

	// --- timing -------------------------------------------------------------
	timings := map[string]map[string]float64{} // case -> impl -> mean seconds
	stddevs := map[string]map[string]float64{}
	for _, c := range cases {
		path := filepath.Join(dir, "hyperfine", c+".json")
		f, err := readHyperfine(path)
		if err != nil {
			continue
		}
		timings[c] = map[string]float64{}
		stddevs[c] = map[string]float64{}
		for _, r := range f.Results {
			impl := implFromCommand(r.Command)
			if impl == "" {
				continue
			}
			timings[c][impl] = r.Mean
			stddevs[c][impl] = r.Stddev
		}
	}

	if len(timings) > 0 {
		b.WriteString("## Time\n\n")
		b.WriteString("Mean ± σ over the hyperfine runs. **Ratio** is the mean divided by the fastest mean in the same row; `1.00` marks the fastest.\n\n")
		b.WriteString("| Case |")
		for _, impl := range impls {
			b.WriteString(" " + implLabels[impl] + " |")
		}
		b.WriteString("\n|---|")
		for range impls {
			b.WriteString("---|")
		}
		b.WriteString("\n")

		for _, c := range cases {
			row, ok := timings[c]
			if !ok {
				continue
			}
			best := 0.0
			for _, impl := range impls {
				v, ok := row[impl]
				if !ok {
					continue
				}
				if best == 0 || v < best {
					best = v
				}
			}
			b.WriteString("| " + caseLabels[c] + " |")
			for _, impl := range impls {
				v, ok := row[impl]
				if !ok {
					b.WriteString(" — |")
					continue
				}
				ratio := ""
				if best > 0 {
					ratio = fmt.Sprintf(" (%.2fx)", v/best)
				}
				fmt.Fprintf(&b, " %s ± %s%s |",
					duration(v), duration(stddevs[c][impl]), ratio)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// --- binary size --------------------------------------------------------
	if sizes, err := readMapInt(filepath.Join(dir, "sizes.json")); err == nil {
		b.WriteString("## Binary size\n\n")
		b.WriteString("| Implementation | Size |\n|---|---|\n")
		for _, impl := range impls {
			v, ok := sizes[impl]
			if !ok {
				continue
			}
			fmt.Fprintf(&b, "| %s | %s |\n", implLabels[impl], bytesHuman(v))
		}
		b.WriteString("\n")
	}

	// --- peak RSS -----------------------------------------------------------
	if rss, err := readNested(filepath.Join(dir, "rss.json")); err == nil {
		b.WriteString("## Peak memory (max RSS)\n\n")
		b.WriteString("| Case |")
		for _, impl := range impls {
			b.WriteString(" " + implLabels[impl] + " |")
		}
		b.WriteString("\n|---|")
		for range impls {
			b.WriteString("---|")
		}
		b.WriteString("\n")
		for _, c := range cases {
			row, ok := rss[c]
			if !ok {
				continue
			}
			b.WriteString("| " + caseLabels[c] + " |")
			for _, impl := range impls {
				v, ok := row[impl]
				if !ok {
					b.WriteString(" — |")
					continue
				}
				fmt.Fprintf(&b, " %s |", bytesHuman(v))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// --- clean build --------------------------------------------------------
	if builds, err := readMapFloat(filepath.Join(dir, "build.json")); err == nil {
		b.WriteString("## Clean build time\n\n")
		b.WriteString("| Implementation | Time |\n|---|---|\n")
		for _, impl := range impls {
			v, ok := builds[impl]
			if !ok {
				continue
			}
			fmt.Fprintf(&b, "| %s | %s |\n", implLabels[impl], duration(v))
		}
		b.WriteString("\n")
	}

	b.WriteString("## How to read this\n\n")
	b.WriteString("- Numbers from a shared CI runner are noisy. Neighbouring workloads, CPU model differences between runs and frequency scaling all move the results, so treat small gaps as noise and compare orders of magnitude.\n")
	b.WriteString("- `--version` and `--help` do no work beyond starting the process, so those rows are a startup cost measurement.\n")
	b.WriteString("- `primes` is the only row with no I/O.\n")
	b.WriteString("- All implementations must emit identical bytes; `make verify` fails the run otherwise.\n")

	return b.String(), nil
}

func row(b *strings.Builder, k, v string) {
	if v == "" {
		return
	}
	fmt.Fprintf(b, "| %s | %s |\n", k, v)
}

// implFromCommand maps a benchmarked command line back to an implementation id.
// The bytecode build must be checked first because its path also contains
// "bench-bun".
func implFromCommand(cmd string) string {
	switch {
	case strings.Contains(cmd, "bench-bun-bytecode"):
		return "bun-bytecode"
	case strings.Contains(cmd, "bench-bun"):
		return "bun"
	case strings.Contains(cmd, "bench-rs"):
		return "rust"
	case strings.Contains(cmd, "bench-go"):
		return "go"
	default:
		return ""
	}
}

func readEnv(path string) (env, error) {
	var e env
	data, err := os.ReadFile(path)
	if err != nil {
		return e, err
	}
	return e, json.Unmarshal(data, &e)
}

func readHyperfine(path string) (hyperfineFile, error) {
	var f hyperfineFile
	data, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(data, &f)
}

func readMapInt(path string) (map[string]int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]int64{}
	return m, json.Unmarshal(data, &m)
}

func readMapFloat(path string) (map[string]float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]float64{}
	return m, json.Unmarshal(data, &m)
}

func readNested(path string) (map[string]map[string]int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]map[string]int64{}
	return m, json.Unmarshal(data, &m)
}

// duration renders seconds with a unit that keeps three significant digits.
func duration(seconds float64) string {
	switch {
	case seconds < 1e-3:
		return fmt.Sprintf("%.1f µs", seconds*1e6)
	case seconds < 1:
		return fmt.Sprintf("%.1f ms", seconds*1e3)
	default:
		return fmt.Sprintf("%.3f s", seconds)
	}
}

func bytesHuman(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
