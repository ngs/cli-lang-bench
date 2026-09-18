//! `bench-rs` is the Rust implementation of the shared benchmark CLI.
//!
//! The three implementations (Rust, Go, Bun + TypeScript) must produce
//! byte-identical output for the `json`, `walk`, `hash` and `primes`
//! subcommands. `make verify` checks that before any benchmark is trusted.
//!
//! Output is compact JSON with a fixed key order, or a single line for `hash`
//! and `primes`. Compact output keeps "identical bytes" easy to guarantee:
//! there is no indentation style to agree on, and no serializer is allowed to
//! reorder keys or reformat numbers.

use std::collections::HashMap;
use std::fmt::Write as FmtWrite;
use std::fs::File;
use std::io::{self, BufWriter, Read, Write};
use std::path::{Path, PathBuf};
use std::process::ExitCode;

use serde::Deserialize;
use sha2::{Digest, Sha256};

const PROG_NAME: &str = "bench-rs";
const VERSION: &str = "0.1.0";

const USAGE: &str = concat!(
    "bench-rs 0.1.0\n",
    "\n",
    "Usage:\n",
    "  bench-rs json <file>    Aggregate a JSON array of records by category\n",
    "  bench-rs walk <dir>     Count files and bytes per extension, recursively\n",
    "  bench-rs hash <file>    Print the SHA-256 of a file as lowercase hex\n",
    "  bench-rs primes <n>     Count primes <= n with a sieve of Eratosthenes\n",
    "  bench-rs --help         Print this message\n",
    "  bench-rs --version      Print the version\n",
);

fn main() -> ExitCode {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let stdout = io::stdout();
    let mut out = BufWriter::new(stdout.lock());

    match run(&args, &mut out) {
        Ok(()) => {
            if let Err(err) = out.flush() {
                eprintln!("{PROG_NAME}: {err}");
                return ExitCode::FAILURE;
            }
            ExitCode::SUCCESS
        }
        Err(err) => {
            let _ = out.flush();
            eprintln!("{PROG_NAME}: {err}");
            ExitCode::FAILURE
        }
    }
}

/// Dispatches a subcommand.
///
/// Argument parsing is hand written on purpose. Every implementation in this
/// repository parses its own arguments with the same few lines, so the startup
/// measurement compares language and runtime overhead rather than the cost of
/// whichever argument parsing library each ecosystem happens to prefer.
fn run(args: &[String], out: &mut impl Write) -> Result<(), String> {
    let Some(first) = args.first() else {
        return Err("missing subcommand (try --help)".into());
    };

    match first.as_str() {
        "--help" | "-h" => out.write_all(USAGE.as_bytes()).map_err(io_err),
        "--version" | "-V" => writeln!(out, "{PROG_NAME} {VERSION}").map_err(io_err),
        "json" => {
            let path = one_arg(args, "json takes exactly one file")?;
            cmd_json(Path::new(path), out)
        }
        "walk" => {
            let path = one_arg(args, "walk takes exactly one directory")?;
            cmd_walk(Path::new(path), out)
        }
        "hash" => {
            let path = one_arg(args, "hash takes exactly one file")?;
            cmd_hash(Path::new(path), out)
        }
        "primes" => {
            let raw = one_arg(args, "primes takes exactly one limit")?;
            let n: i64 = raw
                .parse()
                .map_err(|_| "limit must be a non-negative integer".to_string())?;
            if n < 0 {
                return Err("limit must be a non-negative integer".into());
            }
            writeln!(out, "{}", count_primes(n)).map_err(io_err)
        }
        other => Err(format!("unknown subcommand \"{other}\" (try --help)")),
    }
}

fn one_arg<'a>(args: &'a [String], message: &str) -> Result<&'a str, String> {
    if args.len() != 2 {
        return Err(message.to_string());
    }
    Ok(args[1].as_str())
}

fn io_err(err: io::Error) -> String {
    err.to_string()
}

// ---------------------------------------------------------------- json

#[derive(Deserialize)]
struct Record {
    #[allow(dead_code)]
    id: i64,
    category: String,
    value: i64,
    active: bool,
}

struct Bucket {
    count: i64,
    sum: i64,
    min: i64,
    max: i64,
}

fn cmd_json(path: &Path, out: &mut impl Write) -> Result<(), String> {
    let data = std::fs::read(path).map_err(|e| format!("{}: {e}", path.display()))?;
    let records: Vec<Record> = serde_json::from_slice(&data).map_err(|e| e.to_string())?;

    let mut buckets: HashMap<&str, Bucket> = HashMap::new();
    let mut active: i64 = 0;
    for r in &records {
        if r.active {
            active += 1;
        }
        let entry = buckets.entry(r.category.as_str()).or_insert(Bucket {
            count: 0,
            sum: 0,
            min: r.value,
            max: r.value,
        });
        entry.count += 1;
        entry.sum += r.value;
        if r.value < entry.min {
            entry.min = r.value;
        }
        if r.value > entry.max {
            entry.max = r.value;
        }
    }

    let mut keys: Vec<&str> = buckets.keys().copied().collect();
    keys.sort_unstable();

    let mut buf = String::with_capacity(64 + keys.len() * 96);
    buf.push_str("{\"categories\":[");
    for (i, key) in keys.iter().enumerate() {
        if i > 0 {
            buf.push(',');
        }
        let b = &buckets[key];
        buf.push_str("{\"category\":");
        quote_json(key, &mut buf);
        let _ = write!(
            buf,
            ",\"count\":{},\"sum\":{},\"min\":{},\"max\":{},\"mean\":{}}}",
            b.count,
            b.sum,
            b.min,
            b.max,
            format_mean(b.sum, b.count)
        );
    }
    let _ = write!(
        buf,
        "],\"total\":{},\"active\":{}}}\n",
        records.len(),
        active
    );
    out.write_all(buf.as_bytes()).map_err(io_err)
}

/// Renders `sum / count` with exactly four decimals.
///
/// The arithmetic is integer only, with explicit round-half-up. Formatting a
/// float would leave the rounding mode to each language's formatter, and the
/// three implementations would disagree on ties.
fn format_mean(sum: i64, count: i64) -> String {
    if count == 0 {
        return "0.0000".to_string();
    }
    let negative = sum < 0;
    let sum = sum.abs();
    let scaled = (sum * 10_000 + count / 2) / count;
    let whole = scaled / 10_000;
    let frac = scaled % 10_000;
    let sign = if negative && scaled != 0 { "-" } else { "" };
    format!("{sign}{whole}.{frac:04}")
}

/// Escapes a string the same way in all three implementations. Only the
/// characters JSON requires are escaped; everything else passes through as
/// UTF-8.
fn quote_json(s: &str, buf: &mut String) {
    buf.push('"');
    for ch in s.chars() {
        match ch {
            '"' => buf.push_str("\\\""),
            '\\' => buf.push_str("\\\\"),
            '\n' => buf.push_str("\\n"),
            '\r' => buf.push_str("\\r"),
            '\t' => buf.push_str("\\t"),
            c if (c as u32) < 0x20 => {
                let _ = write!(buf, "\\u{:04x}", c as u32);
            }
            c => buf.push(c),
        }
    }
    buf.push('"');
}

// ---------------------------------------------------------------- walk

fn cmd_walk(dir: &Path, out: &mut impl Write) -> Result<(), String> {
    let mut stats: HashMap<String, (i64, i64)> = HashMap::new();
    let mut files: i64 = 0;
    let mut total: i64 = 0;

    // An explicit stack rather than recursion: deep trees must not depend on
    // the stack size, and the Go and Bun implementations iterate as well.
    let mut stack: Vec<PathBuf> = vec![dir.to_path_buf()];
    while let Some(current) = stack.pop() {
        let entries = std::fs::read_dir(&current)
            .map_err(|e| format!("{}: {e}", current.display()))?;
        for entry in entries {
            let entry = entry.map_err(|e| e.to_string())?;
            // Symlinks are counted as entries but never followed, so the three
            // implementations agree even when a tree contains loops.
            let meta = entry.file_type().map_err(|e| e.to_string())?;
            if meta.is_dir() {
                stack.push(entry.path());
                continue;
            }
            if !meta.is_file() {
                continue;
            }
            let size = entry.metadata().map_err(|e| e.to_string())?.len() as i64;
            let name = entry.file_name();
            let ext = extension(&name.to_string_lossy());
            let slot = stats.entry(ext).or_insert((0, 0));
            slot.0 += 1;
            slot.1 += size;
            files += 1;
            total += size;
        }
    }

    let mut keys: Vec<&String> = stats.keys().collect();
    keys.sort_unstable();

    let mut buf = String::with_capacity(64 + keys.len() * 48);
    buf.push_str("{\"extensions\":[");
    for (i, key) in keys.iter().enumerate() {
        if i > 0 {
            buf.push(',');
        }
        let (count, bytes) = stats[*key];
        buf.push_str("{\"ext\":");
        quote_json(key, &mut buf);
        let _ = write!(buf, ",\"count\":{count},\"bytes\":{bytes}}}");
    }
    let _ = write!(buf, "],\"files\":{files},\"bytes\":{total}}}\n");
    out.write_all(buf.as_bytes()).map_err(io_err)
}

/// Returns the lowercase extension without the dot, or an empty string when
/// the name has none. A leading dot does not start an extension, so
/// ".gitignore" has no extension rather than an extension of "gitignore".
fn extension(name: &str) -> String {
    match name.rfind('.') {
        Some(idx) if idx > 0 && idx + 1 < name.len() => name[idx + 1..].to_lowercase(),
        _ => String::new(),
    }
}

// ---------------------------------------------------------------- hash

fn cmd_hash(path: &Path, out: &mut impl Write) -> Result<(), String> {
    let mut file = File::open(path).map_err(|e| format!("{}: {e}", path.display()))?;
    let mut hasher = Sha256::new();
    let mut buf = vec![0u8; 1 << 20];
    loop {
        let read = file.read(&mut buf).map_err(io_err)?;
        if read == 0 {
            break;
        }
        hasher.update(&buf[..read]);
    }
    let digest = hasher.finalize();

    let mut hex = String::with_capacity(65);
    for byte in digest {
        let _ = write!(hex, "{byte:02x}");
    }
    hex.push('\n');
    out.write_all(hex.as_bytes()).map_err(io_err)
}

// ---------------------------------------------------------------- primes

/// Counts primes `<= n` with a sieve of Eratosthenes.
/// All three implementations use the same odds-only sieve so the benchmark
/// compares code generation rather than algorithm choice.
fn count_primes(n: i64) -> i64 {
    if n < 2 {
        return 0;
    }
    if n == 2 {
        return 1;
    }
    // Index i represents the odd number 2*i+1; index 0 (the number 1) is unused.
    let size = ((n - 1) / 2 + 1) as usize;
    let mut sieve = vec![false; size];
    let mut count: i64 = 1; // 2 is prime and is not represented in the sieve.
    for i in 1..size {
        if sieve[i] {
            continue;
        }
        let p = 2 * i as i64 + 1;
        count += 1;
        if p * p > n {
            continue;
        }
        let mut j = p * p;
        while j <= n {
            sieve[((j - 1) / 2) as usize] = true;
            j += 2 * p;
        }
    }
    count
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn counts_primes() {
        for (n, want) in [
            (0, 0),
            (1, 0),
            (2, 1),
            (3, 2),
            (10, 4),
            (100, 25),
            (1000, 168),
            (1_000_000, 78_498),
        ] {
            assert_eq!(count_primes(n), want, "count_primes({n})");
        }
    }

    #[test]
    fn formats_mean_with_four_decimals() {
        assert_eq!(format_mean(0, 0), "0.0000");
        assert_eq!(format_mean(10, 4), "2.5000");
        assert_eq!(format_mean(1, 3), "0.3333");
        assert_eq!(format_mean(2, 3), "0.6667");
        assert_eq!(format_mean(-10, 4), "-2.5000");
        assert_eq!(format_mean(7, 2), "3.5000");
        assert_eq!(format_mean(100, 1), "100.0000");
    }

    #[test]
    fn extracts_extension() {
        assert_eq!(extension("a.json"), "json");
        assert_eq!(extension("a.JSON"), "json");
        assert_eq!(extension("a.tar.gz"), "gz");
        assert_eq!(extension("noext"), "");
        assert_eq!(extension(".gitignore"), "");
        assert_eq!(extension("trailing."), "");
        assert_eq!(extension(""), "");
    }

    #[test]
    fn escapes_json_strings() {
        let mut buf = String::new();
        quote_json("a\"b\\c\nd", &mut buf);
        assert_eq!(buf, "\"a\\\"b\\\\c\\nd\"");
    }

    #[test]
    fn rejects_bad_arguments() {
        let mut sink = Vec::new();
        for args in [
            vec![],
            vec!["nope".to_string()],
            vec!["json".to_string()],
            vec!["primes".to_string()],
            vec!["primes".to_string(), "-1".to_string()],
            vec!["primes".to_string(), "abc".to_string()],
        ] {
            assert!(run(&args, &mut sink).is_err(), "{args:?} should fail");
        }
    }

    #[test]
    fn prints_version() {
        let mut sink = Vec::new();
        run(&["--version".to_string()], &mut sink).unwrap();
        assert_eq!(String::from_utf8(sink).unwrap(), "bench-rs 0.1.0\n");
    }
}
