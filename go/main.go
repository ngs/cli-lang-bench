// Command bench-go is the Go implementation of the shared benchmark CLI.
//
// The three implementations (Rust, Go, Bun + TypeScript) must produce
// byte-identical output for the json, walk, hash and primes subcommands.
// `make verify` checks that before any benchmark is trusted.
//
// Output is compact JSON with a fixed key order, or a single line for hash
// and primes. Compact output keeps "identical bytes" easy to guarantee:
// there is no indentation style to agree on, and no serializer is allowed to
// reorder keys or reformat numbers.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	progName = "bench-go"
	version  = "0.1.0"
)

const usage = `bench-go ` + version + `

Usage:
  bench-go json <file>    Aggregate a JSON array of records by category
  bench-go walk <dir>     Count files and bytes per extension, recursively
  bench-go hash <file>    Print the SHA-256 of a file as lowercase hex
  bench-go primes <n>     Count primes <= n with a sieve of Eratosthenes
  bench-go --help         Print this message
  bench-go --version      Print the version
`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", progName, err)
		os.Exit(1)
	}
}

// run holds the whole command dispatch so tests can drive it without a process.
//
// Argument parsing is hand written on purpose. Every implementation in this
// repository parses its own arguments with the same few lines, so the startup
// measurement compares language and runtime overhead rather than the cost of
// whichever argument parsing library each ecosystem happens to prefer.
func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("missing subcommand (try --help)")
	}
	switch args[0] {
	case "--help", "-h":
		_, err := io.WriteString(out, usage)
		return err
	case "--version", "-V":
		_, err := fmt.Fprintf(out, "%s %s\n", progName, version)
		return err
	case "json":
		if len(args) != 2 {
			return fmt.Errorf("json takes exactly one file")
		}
		return cmdJSON(args[1], out)
	case "walk":
		if len(args) != 2 {
			return fmt.Errorf("walk takes exactly one directory")
		}
		return cmdWalk(args[1], out)
	case "hash":
		if len(args) != 2 {
			return fmt.Errorf("hash takes exactly one file")
		}
		return cmdHash(args[1], out)
	case "primes":
		if len(args) != 2 {
			return fmt.Errorf("primes takes exactly one limit")
		}
		n, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || n < 0 {
			return fmt.Errorf("limit must be a non-negative integer")
		}
		return cmdPrimes(n, out)
	default:
		return fmt.Errorf("unknown subcommand %q (try --help)", args[0])
	}
}

// ---------------------------------------------------------------- json

type record struct {
	ID       int64  `json:"id"`
	Category string `json:"category"`
	Value    int64  `json:"value"`
	Active   bool   `json:"active"`
}

type bucket struct {
	count int64
	sum   int64
	min   int64
	max   int64
}

func cmdJSON(path string, out io.Writer) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var records []record
	if err := json.Unmarshal(data, &records); err != nil {
		return err
	}

	buckets := make(map[string]*bucket)
	var active int64
	for i := range records {
		r := &records[i]
		if r.Active {
			active++
		}
		b, ok := buckets[r.Category]
		if !ok {
			b = &bucket{min: r.Value, max: r.Value}
			buckets[r.Category] = b
		}
		b.count++
		b.sum += r.Value
		if r.Value < b.min {
			b.min = r.Value
		}
		if r.Value > b.max {
			b.max = r.Value
		}
	}

	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	w := bufio.NewWriter(out)
	w.WriteString(`{"categories":[`)
	for i, k := range keys {
		if i > 0 {
			w.WriteByte(',')
		}
		b := buckets[k]
		w.WriteString(`{"category":`)
		w.WriteString(quoteJSON(k))
		w.WriteString(`,"count":`)
		w.WriteString(strconv.FormatInt(b.count, 10))
		w.WriteString(`,"sum":`)
		w.WriteString(strconv.FormatInt(b.sum, 10))
		w.WriteString(`,"min":`)
		w.WriteString(strconv.FormatInt(b.min, 10))
		w.WriteString(`,"max":`)
		w.WriteString(strconv.FormatInt(b.max, 10))
		w.WriteString(`,"mean":`)
		w.WriteString(FormatMean(b.sum, b.count))
		w.WriteByte('}')
	}
	w.WriteString(`],"total":`)
	w.WriteString(strconv.FormatInt(int64(len(records)), 10))
	w.WriteString(`,"active":`)
	w.WriteString(strconv.FormatInt(active, 10))
	w.WriteString("}\n")
	return w.Flush()
}

// FormatMean renders sum/count with exactly four decimals.
//
// The arithmetic is integer only, with explicit round-half-up. Formatting a
// float64 would leave the rounding mode to each language's printf, and the
// three implementations would disagree on ties.
func FormatMean(sum, count int64) string {
	if count == 0 {
		return "0.0000"
	}
	neg := false
	if sum < 0 {
		neg = true
		sum = -sum
	}
	scaled := (sum*10000 + count/2) / count
	whole := scaled / 10000
	frac := scaled % 10000
	sign := ""
	if neg && scaled != 0 {
		sign = "-"
	}
	return fmt.Sprintf("%s%d.%04d", sign, whole, frac)
}

// quoteJSON escapes a string the same way in all three implementations.
// Only the characters JSON requires are escaped; everything else is passed
// through as UTF-8.
func quoteJSON(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ---------------------------------------------------------------- walk

type extStat struct {
	count int64
	bytes int64
}

func cmdWalk(dir string, out io.Writer) error {
	stats := make(map[string]*extStat)
	var files, total int64

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		// Symlinks are counted as entries but never followed, so the three
		// implementations agree even when a tree contains loops.
		if !info.Mode().IsRegular() {
			return nil
		}
		ext := Extension(d.Name())
		s, ok := stats[ext]
		if !ok {
			s = &extStat{}
			stats[ext] = s
		}
		s.count++
		s.bytes += info.Size()
		files++
		total += info.Size()
		return nil
	})
	if err != nil {
		return err
	}

	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	w := bufio.NewWriter(out)
	w.WriteString(`{"extensions":[`)
	for i, k := range keys {
		if i > 0 {
			w.WriteByte(',')
		}
		w.WriteString(`{"ext":`)
		w.WriteString(quoteJSON(k))
		w.WriteString(`,"count":`)
		w.WriteString(strconv.FormatInt(stats[k].count, 10))
		w.WriteString(`,"bytes":`)
		w.WriteString(strconv.FormatInt(stats[k].bytes, 10))
		w.WriteByte('}')
	}
	w.WriteString(`],"files":`)
	w.WriteString(strconv.FormatInt(files, 10))
	w.WriteString(`,"bytes":`)
	w.WriteString(strconv.FormatInt(total, 10))
	w.WriteString("}\n")
	return w.Flush()
}

// Extension returns the lowercase extension without the dot, or "" when the
// name has none. A leading dot does not start an extension, so ".gitignore"
// has no extension rather than an extension of "gitignore".
func Extension(name string) string {
	idx := strings.LastIndexByte(name, '.')
	if idx <= 0 || idx == len(name)-1 {
		return ""
	}
	return strings.ToLower(name[idx+1:])
}

// ---------------------------------------------------------------- hash

func cmdHash(path string, out io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, 1<<20)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%x\n", h.Sum(nil))
	return err
}

// ---------------------------------------------------------------- primes

func cmdPrimes(n int64, out io.Writer) error {
	_, err := fmt.Fprintf(out, "%d\n", CountPrimes(n))
	return err
}

// CountPrimes counts primes <= n with a sieve of Eratosthenes.
// All three implementations use the same odds-only sieve so the benchmark
// compares code generation rather than algorithm choice.
func CountPrimes(n int64) int64 {
	if n < 2 {
		return 0
	}
	if n == 2 {
		return 1
	}
	// Index i represents the odd number 2*i+1; index 0 (the number 1) is unused.
	size := (n-1)/2 + 1
	sieve := make([]bool, size)
	count := int64(1) // 2 is prime and is not represented in the sieve.
	for i := int64(1); i < size; i++ {
		if sieve[i] {
			continue
		}
		p := 2*i + 1
		count++
		if p*p > n {
			continue
		}
		for j := p * p; j <= n; j += 2 * p {
			sieve[(j-1)/2] = true
		}
	}
	return count
}
