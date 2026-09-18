package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCountPrimes(t *testing.T) {
	cases := []struct {
		n    int64
		want int64
	}{
		{0, 0},
		{1, 0},
		{2, 1},
		{3, 2},
		{10, 4},
		{100, 25},
		{1000, 168},
		{1000000, 78498},
	}
	for _, c := range cases {
		if got := CountPrimes(c.n); got != c.want {
			t.Errorf("CountPrimes(%d) = %d, want %d", c.n, got, c.want)
		}
	}
}

func TestFormatMean(t *testing.T) {
	cases := []struct {
		sum, count int64
		want       string
	}{
		{0, 0, "0.0000"},
		{10, 4, "2.5000"},
		{1, 3, "0.3333"},
		{2, 3, "0.6667"},
		{-10, 4, "-2.5000"},
		{7, 2, "3.5000"},
		{100, 1, "100.0000"},
	}
	for _, c := range cases {
		if got := FormatMean(c.sum, c.count); got != c.want {
			t.Errorf("FormatMean(%d, %d) = %s, want %s", c.sum, c.count, got, c.want)
		}
	}
}

func TestExtension(t *testing.T) {
	cases := map[string]string{
		"a.json":      "json",
		"a.JSON":      "json",
		"a.tar.gz":    "gz",
		"noext":       "",
		".gitignore":  "",
		"trailing.":   "",
		"UPPER.TXT":   "txt",
		"a.b.c.md":    "md",
		"":            "",
	}
	for name, want := range cases {
		if got := Extension(name); got != want {
			t.Errorf("Extension(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestRunJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "in.json")
	body := `[
	  {"id":1,"category":"b","value":10,"active":true},
	  {"id":2,"category":"a","value":1,"active":false},
	  {"id":3,"category":"a","value":2,"active":true}
	]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"json", path}, &out); err != nil {
		t.Fatal(err)
	}
	want := `{"categories":[` +
		`{"category":"a","count":2,"sum":3,"min":1,"max":2,"mean":1.5000},` +
		`{"category":"b","count":1,"sum":10,"min":10,"max":10,"mean":10.0000}` +
		`],"total":3,"active":2}` + "\n"
	if out.String() != want {
		t.Errorf("json output mismatch\n got: %s\nwant: %s", out.String(), want)
	}
}

func TestRunHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "in.bin")
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"hash", path}, &out); err != nil {
		t.Fatal(err)
	}
	// Known SHA-256 of "abc".
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad\n"
	if out.String() != want {
		t.Errorf("hash = %q, want %q", out.String(), want)
	}
}

func TestRunWalk(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("123"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "noext"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"walk", dir}, &out); err != nil {
		t.Fatal(err)
	}
	want := `{"extensions":[` +
		`{"ext":"","count":1,"bytes":1},` +
		`{"ext":"txt","count":2,"bytes":8}` +
		`],"files":3,"bytes":9}` + "\n"
	if out.String() != want {
		t.Errorf("walk output mismatch\n got: %s\nwant: %s", out.String(), want)
	}
}

func TestRunErrors(t *testing.T) {
	var out bytes.Buffer
	for _, args := range [][]string{
		{},
		{"nope"},
		{"json"},
		{"primes"},
		{"primes", "-1"},
		{"primes", "abc"},
	} {
		if err := run(args, &out); err == nil {
			t.Errorf("run(%v) should fail", args)
		}
	}
}

func TestRunVersionAndHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"--version"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != progName+" "+version+"\n" {
		t.Errorf("unexpected version line: %q", out.String())
	}

	out.Reset()
	if err := run([]string{"--help"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Errorf("help should contain a usage section")
	}
}
