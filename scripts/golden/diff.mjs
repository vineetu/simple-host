#!/usr/bin/env node
// Readable per-request diff between two golden snapshots (see run.mjs).
//
//   node scripts/golden/diff.mjs A.json B.json
//
// Exit 0 if every request matches; exit 1 on any difference (a request
// missing from one side, a status mismatch, a header mismatch, or a body
// mismatch), after printing every difference found -- not just the first.
import { readFileSync } from 'node:fs';

function load(file) {
  const items = JSON.parse(readFileSync(file, 'utf8'));
  const byName = new Map();
  for (const item of items) byName.set(item.name, item);
  return { items, byName };
}

function stable(value) {
  // Same key order every time (finalizeCall in run.mjs already sorts JSON
  // object keys before this, but headers/the top-level record are built in
  // a fixed field order already, so this is only for safety).
  return JSON.stringify(value, Object.keys(value || {}).sort());
}

function describe(path, a, b) {
  return `    ${path}:\n      A: ${JSON.stringify(a)}\n      B: ${JSON.stringify(b)}`;
}

// diffValue walks two normalized JSON values in parallel and returns a list
// of human-readable differences, each anchored to a dotted/bracketed path.
function diffValue(path, a, b, out) {
  if (a === b) return;
  const aType = Array.isArray(a) ? 'array' : typeof a;
  const bType = Array.isArray(b) ? 'array' : typeof b;
  if (aType !== bType || a === null || b === null) {
    if (a !== b) out.push(describe(path, a, b));
    return;
  }
  if (aType === 'object') {
    const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
    for (const k of [...keys].sort()) {
      diffValue(`${path}.${k}`, a[k], b[k], out);
    }
    return;
  }
  if (aType === 'array') {
    const len = Math.max(a.length, b.length);
    for (let i = 0; i < len; i++) {
      diffValue(`${path}[${i}]`, a[i], b[i], out);
    }
    return;
  }
  out.push(describe(path, a, b));
}

function diffRequest(a, b) {
  const out = [];
  if (a.status !== b.status) out.push(describe('status', a.status, b.status));
  if (a.method !== b.method) out.push(describe('method', a.method, b.method));
  if (a.path !== b.path) out.push(describe('path', a.path, b.path));
  diffValue('headers', a.headers || {}, b.headers || {}, out);
  diffValue('body', a.body ?? null, b.body ?? null, out);
  return out;
}

function main() {
  const [fileA, fileB] = process.argv.slice(2);
  if (!fileA || !fileB) {
    console.error('usage: node diff.mjs A.json B.json');
    process.exit(2);
  }
  const a = load(fileA);
  const b = load(fileB);

  let differences = 0;
  const allNames = new Set([...a.byName.keys(), ...b.byName.keys()]);
  for (const name of [...allNames].sort()) {
    const ra = a.byName.get(name);
    const rb = b.byName.get(name);
    if (!ra) {
      console.log(`+ ${name}: present only in ${fileB}`);
      differences++;
      continue;
    }
    if (!rb) {
      console.log(`- ${name}: present only in ${fileA}`);
      differences++;
      continue;
    }
    if (stable(ra) === stable(rb)) continue;
    const reqDiffs = diffRequest(ra, rb);
    if (reqDiffs.length === 0) continue; // a key-order-only artifact, not a real difference
    console.log(`! ${name}  (${ra.method} ${ra.path})`);
    for (const line of reqDiffs) console.log(line);
    differences += reqDiffs.length;
  }

  if (differences === 0) {
    console.log(`ok: ${a.items.length} requests, identical`);
    process.exit(0);
  }
  console.log(`\nFAIL: ${differences} difference(s) across ${allNames.size} requests`);
  process.exit(1);
}

main();
