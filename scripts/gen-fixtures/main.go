// Command gen-fixtures writes the benchmark inputs.
//
// Everything is derived from a fixed seed, so the same command produces the
// same bytes on every machine. The outputs are large, so they are gitignored
// and regenerated instead of committed.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
)

var categories = []string{
	"alpha", "beta", "delta", "epsilon", "gamma",
	"lambda", "omega", "sigma", "theta", "zeta",
}

// extensions for the generated tree. ASCII only, so that sorting by bytes
// (Rust, Go) and by UTF-16 code units (JavaScript) agree.
//
// Deliberately no ".go": the tree lives inside this module, and a directory of
// files that end in .go but are not Go source makes `go vet ./...` and
// `go build ./...` fail on generated data. The extensions only need to be
// varied, not meaningful.
var extensions = []string{"css", "html", "json", "md", "rs", "ts", "txt"}

func main() {
	var (
		dir     = flag.String("dir", "fixtures", "output directory")
		records = flag.Int("records", 200000, "number of JSON records")
		files   = flag.Int("files", 4000, "number of files in the walk tree")
		blobMiB = flag.Int("blob-mib", 64, "size of the file used by `hash`, in MiB")
		seed    = flag.Int64("seed", 20260918, "PRNG seed")
	)
	flag.Parse()

	if err := generate(*dir, *records, *files, *blobMiB, *seed); err != nil {
		fmt.Fprintf(os.Stderr, "gen-fixtures: %v\n", err)
		os.Exit(1)
	}
}

func generate(dir string, records, files, blobMiB int, seed int64) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeRecords(filepath.Join(dir, "records.json"), records, seed); err != nil {
		return err
	}
	if err := writeTree(filepath.Join(dir, "tree"), files, seed+1); err != nil {
		return err
	}
	if err := writeBlob(filepath.Join(dir, "blob.bin"), blobMiB, seed+2); err != nil {
		return err
	}
	fmt.Printf("fixtures written to %s (records=%d files=%d blob=%dMiB seed=%d)\n",
		dir, records, files, blobMiB, seed)
	return nil
}

// writeRecords emits a JSON array of objects. The shape matches what the
// `json` subcommand aggregates: id, category, value, active.
func writeRecords(path string, n int, seed int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	rng := rand.New(rand.NewSource(seed))
	w := bufio.NewWriterSize(f, 1<<20)
	w.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			w.WriteByte(',')
		}
		cat := categories[rng.Intn(len(categories))]
		value := rng.Intn(1_000_000)
		active := rng.Intn(2) == 1
		w.WriteString(`{"id":`)
		w.WriteString(strconv.Itoa(i))
		w.WriteString(`,"category":"`)
		w.WriteString(cat)
		w.WriteString(`","value":`)
		w.WriteString(strconv.Itoa(value))
		w.WriteString(`,"active":`)
		w.WriteString(strconv.FormatBool(active))
		w.WriteByte('}')
	}
	w.WriteByte(']')
	w.WriteByte('\n')
	return w.Flush()
}

// writeTree builds a nested directory tree with files of varied extensions and
// sizes, plus a few extensionless files so that the "" bucket is exercised.
func writeTree(root string, files int, seed int64) error {
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	rng := rand.New(rand.NewSource(seed))

	// A fixed fan-out keeps the tree shape identical across runs.
	const dirsPerLevel = 8
	const depth = 3

	dirs := []string{root}
	for level := 0; level < depth; level++ {
		var next []string
		for _, parent := range dirs {
			for i := 0; i < dirsPerLevel; i++ {
				child := filepath.Join(parent, fmt.Sprintf("d%02d", i))
				if err := os.MkdirAll(child, 0o755); err != nil {
					return err
				}
				next = append(next, child)
			}
		}
		dirs = append(dirs, next...)
	}

	body := make([]byte, 8192)
	for i := range body {
		body[i] = byte('a' + rng.Intn(26))
	}

	for i := 0; i < files; i++ {
		dir := dirs[rng.Intn(len(dirs))]
		size := 64 + rng.Intn(4096)
		var name string
		if i%37 == 0 {
			name = fmt.Sprintf("noext%05d", i)
		} else {
			name = fmt.Sprintf("f%05d.%s", i, extensions[rng.Intn(len(extensions))])
		}
		if err := os.WriteFile(filepath.Join(dir, name), body[:size], 0o644); err != nil {
			return err
		}
	}
	return nil
}

// writeBlob writes the file that `hash` reads. The content is pseudo-random so
// it does not compress, which keeps the I/O cost honest.
func writeBlob(path string, mib int, seed int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	rng := rand.New(rand.NewSource(seed))
	chunk := make([]byte, 1<<20)
	w := bufio.NewWriterSize(f, 1<<20)
	for i := 0; i < mib; i++ {
		if _, err := rng.Read(chunk); err != nil {
			return err
		}
		if _, err := w.Write(chunk); err != nil {
			return err
		}
	}
	return w.Flush()
}
