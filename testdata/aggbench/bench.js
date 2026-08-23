// bench.js - drives aggbench.wasm under node.
//
// The module owns its input, so this only starts passes and reads the clock.
// Every pass is checksummed: a toolchain that produces a different cell buffer
// is reported as WRONG rather than fast, which is the failure mode that
// matters when the thing under test is a compiler.
//
// Usage:
//   node bench.js aggbench.wasm [--iters N] [--warmup N] [--cells N]
//                              [--pages-per-cell N] [--mark] [--json]
//
// wasm_exec.js is taken from the toolchain that built the module: pass
// --exec PATH, or let it default to $GOROOT/lib/wasm/wasm_exec.js.

'use strict';
const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');

function parseArgs(argv) {
  const o = {
    wasm: null, iters: 40, warmup: 5, cells: 283326, ppc: 16,
    mark: false, json: false, exec: null,
  };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const num = () => Number(argv[++i]);
    switch (a) {
      case '--iters': o.iters = num(); break;
      case '--warmup': o.warmup = num(); break;
      case '--cells': o.cells = num(); break;
      case '--pages-per-cell': o.ppc = num(); break;
      case '--exec': o.exec = argv[++i]; break;
      case '--mark': o.mark = true; break;
      case '--json': o.json = true; break;
      default:
        if (a.startsWith('--')) { console.error(`unknown flag ${a}`); process.exit(2); }
        o.wasm = a;
    }
  }
  if (!o.wasm) { console.error('usage: node bench.js aggbench.wasm [flags]'); process.exit(2); }
  return o;
}

function findWasmExec(explicit) {
  if (explicit) return explicit;
  let goroot = process.env.GOROOT;
  if (!goroot) {
    try {
      goroot = execFileSync('go', ['env', 'GOROOT'], { encoding: 'utf8' }).trim();
    } catch {
      console.error('cannot locate wasm_exec.js: set GOROOT or pass --exec');
      process.exit(2);
    }
  }
  return path.join(goroot, 'lib', 'wasm', 'wasm_exec.js');
}

function stats(xs) {
  const s = [...xs].sort((a, b) => a - b);
  const at = (q) => s[Math.min(s.length - 1, Math.floor(q * s.length))];
  return { min: s[0], p50: at(0.5), p90: at(0.9), max: s[s.length - 1] };
}

async function main() {
  const opt = parseArgs(process.argv.slice(2));
  // wasm_exec.js is a plain script that installs globalThis.Go.
  (0, eval)(fs.readFileSync(findWasmExec(opt.exec), 'utf8'));

  const bytes = fs.readFileSync(opt.wasm);
  const t0 = performance.now();
  const go = new globalThis.Go();
  const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
  const compileMs = performance.now() - t0;
  go.run(instance); // main() installs the exports, then blocks in select{}
  await new Promise((r) => setTimeout(r, 50));
  if (typeof globalThis.bench_aggregate !== 'function') {
    console.error('module did not install its exports');
    process.exit(1);
  }

  const pages = globalThis.bench_setup(opt.cells, opt.ppc, opt.mark);

  const time = (fn, n) => {
    const ts = [];
    for (let i = 0; i < n; i++) {
      const a = performance.now();
      fn();
      ts.push(performance.now() - a);
    }
    return ts;
  };

  time(globalThis.bench_aggregate, opt.warmup);
  const agg = stats(time(globalThis.bench_aggregate, opt.iters));
  const checksum = globalThis.bench_checksum();

  time(globalThis.bench_decode, opt.warmup);
  const dec = stats(time(globalThis.bench_decode, opt.iters));

  const out = {
    wasm: path.basename(opt.wasm),
    pages, cells: opt.cells, pagesPerCell: opt.ppc, mark: opt.mark,
    moduleBytes: bytes.length,
    compileMs: +compileMs.toFixed(1),
    aggregateMs: agg, decodeMs: dec,
    checksum,
  };
  if (opt.json) {
    console.log(JSON.stringify(out));
  } else {
    const f = (x) => x.toFixed(2).padStart(7);
    console.log(`${out.wasm}: ${pages} pages -> ${opt.cells} cells @ ${opt.ppc} pages` +
      `${opt.mark ? ' (mark set)' : ''}`);
    console.log(`  module ${(bytes.length / 1e6).toFixed(2)} MB, compile+instantiate ${out.compileMs} ms`);
    console.log(`  aggregate   min ${f(agg.min)}  p50 ${f(agg.p50)}  p90 ${f(agg.p90)}  max ${f(agg.max)} ms`);
    console.log(`  rle decode  min ${f(dec.min)}  p50 ${f(dec.p50)}  p90 ${f(dec.p90)}  max ${f(dec.max)} ms`);
    console.log(`  checksum ${checksum}  (must match across toolchains)`);
  }
  process.exit(0);
}

main();
