/**
 * `bench-bun` is the Bun + TypeScript implementation of the shared benchmark CLI.
 *
 * The three implementations (Rust, Go, Bun + TypeScript) must produce
 * byte-identical output for the `json`, `walk`, `hash` and `primes`
 * subcommands. `make verify` checks that before any benchmark is trusted.
 *
 * Output is compact JSON with a fixed key order, or a single line for `hash`
 * and `primes`. Compact output keeps "identical bytes" easy to guarantee:
 * there is no indentation style to agree on, and no serializer is allowed to
 * reorder keys or reformat numbers.
 */

import { createHash } from "node:crypto";
import { readdirSync, statSync, lstatSync, createReadStream } from "node:fs";
import { join } from "node:path";

const PROG_NAME = "bench-bun";
const VERSION = "0.1.0";

const USAGE = `bench-bun ${VERSION}

Usage:
  bench-bun json <file>    Aggregate a JSON array of records by category
  bench-bun walk <dir>     Count files and bytes per extension, recursively
  bench-bun hash <file>    Print the SHA-256 of a file as lowercase hex
  bench-bun primes <n>     Count primes <= n with a sieve of Eratosthenes
  bench-bun --help         Print this message
  bench-bun --version      Print the version
`;

interface Record {
  id: number;
  category: string;
  value: number;
  active: boolean;
}

/**
 * Renders `sum / count` with exactly four decimals.
 *
 * The arithmetic is integer only (via BigInt), with explicit round-half-up.
 * Formatting a double would leave the rounding mode to the runtime, and the
 * three implementations would disagree on ties.
 */
export function formatMean(sum: number, count: number): string {
  if (count === 0) return "0.0000";
  const negative = sum < 0;
  const s = BigInt(negative ? -sum : sum);
  const c = BigInt(count);
  const scaled = (s * 10000n + c / 2n) / c;
  const whole = scaled / 10000n;
  const frac = scaled % 10000n;
  const sign = negative && scaled !== 0n ? "-" : "";
  return `${sign}${whole}.${frac.toString().padStart(4, "0")}`;
}

/**
 * Returns the lowercase extension without the dot, or an empty string when the
 * name has none. A leading dot does not start an extension, so ".gitignore" has
 * no extension rather than an extension of "gitignore".
 */
export function extension(name: string): string {
  const idx = name.lastIndexOf(".");
  if (idx <= 0 || idx === name.length - 1) return "";
  return name.slice(idx + 1).toLowerCase();
}

/**
 * Escapes a string the same way in all three implementations. Only the
 * characters JSON requires are escaped; everything else passes through as
 * UTF-8.
 */
export function quoteJson(s: string): string {
  let out = '"';
  for (const ch of s) {
    switch (ch) {
      case '"':
        out += '\\"';
        break;
      case "\\":
        out += "\\\\";
        break;
      case "\n":
        out += "\\n";
        break;
      case "\r":
        out += "\\r";
        break;
      case "\t":
        out += "\\t";
        break;
      default: {
        const code = ch.codePointAt(0) ?? 0;
        out += code < 0x20 ? `\\u${code.toString(16).padStart(4, "0")}` : ch;
      }
    }
  }
  return out + '"';
}

/**
 * Counts primes `<= n` with a sieve of Eratosthenes.
 * All three implementations use the same odds-only sieve so the benchmark
 * compares code generation rather than algorithm choice.
 */
export function countPrimes(n: number): number {
  if (n < 2) return 0;
  if (n === 2) return 1;
  // Index i represents the odd number 2*i+1; index 0 (the number 1) is unused.
  const size = Math.floor((n - 1) / 2) + 1;
  const sieve = new Uint8Array(size);
  let count = 1; // 2 is prime and is not represented in the sieve.
  for (let i = 1; i < size; i++) {
    if (sieve[i] === 1) continue;
    const p = 2 * i + 1;
    count++;
    if (p * p > n) continue;
    for (let j = p * p; j <= n; j += 2 * p) {
      sieve[(j - 1) / 2] = 1;
    }
  }
  return count;
}

export function aggregateJson(records: Record[]): string {
  const buckets = new Map<
    string,
    { count: number; sum: number; min: number; max: number }
  >();
  let active = 0;

  for (const r of records) {
    if (r.active) active++;
    let b = buckets.get(r.category);
    if (b === undefined) {
      b = { count: 0, sum: 0, min: r.value, max: r.value };
      buckets.set(r.category, b);
    }
    b.count++;
    b.sum += r.value;
    if (r.value < b.min) b.min = r.value;
    if (r.value > b.max) b.max = r.value;
  }

  // Sort by UTF-16 code unit order. Category names are ASCII in the generated
  // fixtures, where this matches Rust's and Go's byte order sorting.
  const keys = [...buckets.keys()].sort();

  const parts: string[] = [];
  for (const key of keys) {
    const b = buckets.get(key)!;
    parts.push(
      `{"category":${quoteJson(key)},"count":${b.count},"sum":${b.sum},` +
        `"min":${b.min},"max":${b.max},"mean":${formatMean(b.sum, b.count)}}`,
    );
  }
  return `{"categories":[${parts.join(",")}],"total":${records.length},"active":${active}}\n`;
}

export function walkDir(dir: string): string {
  const stats = new Map<string, { count: number; bytes: number }>();
  let files = 0;
  let total = 0;

  // An explicit stack rather than recursion: deep trees must not depend on the
  // stack size, and the Rust and Go implementations iterate as well.
  const stack: string[] = [dir];
  while (stack.length > 0) {
    const current = stack.pop()!;
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const full = join(current, entry.name);
      if (entry.isDirectory()) {
        stack.push(full);
        continue;
      }
      // Symlinks are counted as entries but never followed, so the three
      // implementations agree even when a tree contains loops.
      if (!entry.isFile()) continue;
      const size = lstatSync(full).size;
      const ext = extension(entry.name);
      let slot = stats.get(ext);
      if (slot === undefined) {
        slot = { count: 0, bytes: 0 };
        stats.set(ext, slot);
      }
      slot.count++;
      slot.bytes += size;
      files++;
      total += size;
    }
  }

  const keys = [...stats.keys()].sort();
  const parts = keys.map((key) => {
    const s = stats.get(key)!;
    return `{"ext":${quoteJson(key)},"count":${s.count},"bytes":${s.bytes}}`;
  });
  return `{"extensions":[${parts.join(",")}],"files":${files},"bytes":${total}}\n`;
}

async function hashFile(path: string): Promise<string> {
  // Streamed rather than read whole, to measure I/O the same way as the other
  // implementations.
  const hash = createHash("sha256");
  const stream = createReadStream(path, { highWaterMark: 1 << 20 });
  for await (const chunk of stream) {
    hash.update(chunk as Uint8Array);
  }
  return hash.digest("hex") + "\n";
}

/**
 * Dispatches a subcommand and returns the text to print.
 *
 * Argument parsing is hand written on purpose. Every implementation in this
 * repository parses its own arguments with the same few lines, so the startup
 * measurement compares language and runtime overhead rather than the cost of
 * whichever argument parsing library each ecosystem happens to prefer.
 */
export async function run(args: string[]): Promise<string> {
  const first = args[0];
  if (first === undefined) throw new Error("missing subcommand (try --help)");

  switch (first) {
    case "--help":
    case "-h":
      return USAGE;
    case "--version":
    case "-V":
      return `${PROG_NAME} ${VERSION}\n`;
    case "json": {
      const path = oneArg(args, "json takes exactly one file");
      const text = await Bun.file(path).text();
      return aggregateJson(JSON.parse(text) as Record[]);
    }
    case "walk": {
      const path = oneArg(args, "walk takes exactly one directory");
      statSync(path); // fail early with a clear error when the path is missing
      return walkDir(path);
    }
    case "hash": {
      const path = oneArg(args, "hash takes exactly one file");
      return await hashFile(path);
    }
    case "primes": {
      const raw = oneArg(args, "primes takes exactly one limit");
      if (!/^\d+$/.test(raw)) {
        throw new Error("limit must be a non-negative integer");
      }
      return `${countPrimes(Number(raw))}\n`;
    }
    default:
      throw new Error(`unknown subcommand "${first}" (try --help)`);
  }
}

function oneArg(args: string[], message: string): string {
  if (args.length !== 2) throw new Error(message);
  return args[1]!;
}

// Wrapped in a function rather than using top-level await: `bun build
// --bytecode` compiles to a module format that does not allow it.
async function main(): Promise<void> {
  try {
    process.stdout.write(await run(Bun.argv.slice(2)));
  } catch (err) {
    process.stderr.write(
      `${PROG_NAME}: ${err instanceof Error ? err.message : String(err)}\n`,
    );
    process.exit(1);
  }
}

if (import.meta.main) {
  void main();
}
