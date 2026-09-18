import { describe, expect, test } from "bun:test";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  aggregateJson,
  countPrimes,
  extension,
  formatMean,
  quoteJson,
  run,
  walkDir,
} from "./main.ts";

describe("countPrimes", () => {
  test.each([
    [0, 0],
    [1, 0],
    [2, 1],
    [3, 2],
    [10, 4],
    [100, 25],
    [1000, 168],
    [1000000, 78498],
  ])("countPrimes(%i) === %i", (n, want) => {
    expect(countPrimes(n)).toBe(want);
  });
});

describe("formatMean", () => {
  test.each([
    [0, 0, "0.0000"],
    [10, 4, "2.5000"],
    [1, 3, "0.3333"],
    [2, 3, "0.6667"],
    [-10, 4, "-2.5000"],
    [7, 2, "3.5000"],
    [100, 1, "100.0000"],
  ])("formatMean(%i, %i) === %s", (sum, count, want) => {
    expect(formatMean(sum, count)).toBe(want);
  });
});

describe("extension", () => {
  test.each([
    ["a.json", "json"],
    ["a.JSON", "json"],
    ["a.tar.gz", "gz"],
    ["noext", ""],
    [".gitignore", ""],
    ["trailing.", ""],
    ["", ""],
  ])("extension(%p) === %p", (name, want) => {
    expect(extension(name)).toBe(want);
  });
});

test("quoteJson escapes only what JSON requires", () => {
  expect(quoteJson('a"b\\c\nd')).toBe('"a\\"b\\\\c\\nd"');
});

test("aggregateJson sorts categories and counts active records", () => {
  const out = aggregateJson([
    { id: 1, category: "b", value: 10, active: true },
    { id: 2, category: "a", value: 1, active: false },
    { id: 3, category: "a", value: 2, active: true },
  ]);
  expect(out).toBe(
    '{"categories":[' +
      '{"category":"a","count":2,"sum":3,"min":1,"max":2,"mean":1.5000},' +
      '{"category":"b","count":1,"sum":10,"min":10,"max":10,"mean":10.0000}' +
      '],"total":3,"active":2}\n',
  );
});

test("walkDir counts files and bytes per extension", () => {
  const dir = mkdtempSync(join(tmpdir(), "bench-bun-"));
  try {
    mkdirSync(join(dir, "sub"));
    writeFileSync(join(dir, "a.txt"), "12345");
    writeFileSync(join(dir, "sub", "b.txt"), "123");
    writeFileSync(join(dir, "sub", "noext"), "1");
    expect(walkDir(dir)).toBe(
      '{"extensions":[' +
        '{"ext":"","count":1,"bytes":1},' +
        '{"ext":"txt","count":2,"bytes":8}' +
        '],"files":3,"bytes":9}\n',
    );
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("hash matches the known SHA-256 of abc", async () => {
  const dir = mkdtempSync(join(tmpdir(), "bench-bun-"));
  try {
    const path = join(dir, "in.bin");
    writeFileSync(path, "abc");
    expect(await run(["hash", path])).toBe(
      "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad\n",
    );
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("--version prints the program name and version", async () => {
  expect(await run(["--version"])).toBe("bench-bun 0.1.0\n");
});

test("--help prints a usage section", async () => {
  expect(await run(["--help"])).toContain("Usage:");
});

describe("argument errors", () => {
  test.each([
    [[] as string[]],
    [["nope"]],
    [["json"]],
    [["primes"]],
    [["primes", "-1"]],
    [["primes", "abc"]],
  ])("run(%p) rejects", async (args) => {
    await expect(run(args)).rejects.toThrow();
  });
});
