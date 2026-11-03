#!/usr/bin/env node
// Readable per-request diff between two golden snapshots (see run.mjs).
//
//   node scripts/golden/diff.mjs A.json B.json
//
// Exit 0 if every request matches; exit 1 on any difference (a duplicate or
// reordered request name, a request missing from one side, a status
// mismatch, a header mismatch, or a body mismatch -- down to any nested
// field), after printing every difference found -- not just the first.
import { readFileSync } from 'node:fs';

function load(file) {
  return JSON.parse(readFileSync(file, 'utf8'));
}

// findDuplicates returns every name that appears more than once in items,
// in first-seen order.
function findDuplicates(items) {
  const seen = new Set();
  const dups = [];
  for (const item of items) {
    if (seen.has(item.name)) {
      if (!dups.includes(item.name)) dups.push(item.name);
    } else {
      seen.add(item.name);
    }
  }
  return dups;
}

function describe(path, a, b) {
  return `    ${path}:\n      A: ${JSON.stringify(a)}\n      B: ${JSON.stringify(b)}`;
}

// diffValue walks two normalized JSON values in parallel and returns a list
// of human-readable differences, each anchored to a dotted/bracketed path.
// This always walks the full structure -- there is no shortcut comparison
// first, because a shortcut that doesn't look at every nested key is exactly
// how a changed nested field (a header's value, a body's nested object key)
// slips past as "identical".
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
  const itemsA = load(fileA);
  const itemsB = load(fileB);

  let differences = 0;

  // Duplicate names inside one file: the comparator below only knows named
  // requests, so a duplicate (two recorded requests sharing a name) would
  // otherwise be silently collapsed into one.
  for (const name of findDuplicates(itemsA)) {
    console.log(`duplicate request name in ${fileA}: ${name}`);
    differences++;
  }
  for (const name of findDuplicates(itemsB)) {
    console.log(`duplicate request name in ${fileB}: ${name}`);
    differences++;
  }

  // Sequence: the replay order itself is part of what a baseline pins (a
  // request moving earlier/later, or a request being dropped mid-sequence,
  // is a real change even when every individual request's content still
  // matches something on the other side). Report only the first divergence
  // -- usually everything after it has also shifted, and printing every one
  // of those would bury the actual cause.
  const namesA = itemsA.map((i) => i.name);
  const namesB = itemsB.map((i) => i.name);
  const seqLen = Math.max(namesA.length, namesB.length);
  for (let i = 0; i < seqLen; i++) {
    if (namesA[i] !== namesB[i]) {
      console.log(
        `sequence diverges at position ${i}: ${fileA} has ${namesA[i] ?? '(none, ' + namesA.length + ' total)'}, ` +
          `${fileB} has ${namesB[i] ?? '(none, ' + namesB.length + ' total)'}`
      );
      differences++;
      break;
    }
  }

  // Content: every request present on either side, compared by name
  // (duplicates above already take the last occurrence here, which is fine
  // since a duplicate is already reported as its own failure).
  const byNameA = new Map(itemsA.map((i) => [i.name, i]));
  const byNameB = new Map(itemsB.map((i) => [i.name, i]));
  const allNames = new Set([...byNameA.keys(), ...byNameB.keys()]);
  for (const name of [...allNames].sort()) {
    const ra = byNameA.get(name);
    const rb = byNameB.get(name);
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
    const reqDiffs = diffRequest(ra, rb);
    if (reqDiffs.length === 0) continue;
    console.log(`! ${name}  (${ra.method} ${ra.path})`);
    for (const line of reqDiffs) console.log(line);
    differences += reqDiffs.length;
  }

  if (differences === 0) {
    console.log(`ok: ${itemsA.length} requests, identical`);
    process.exit(0);
  }
  console.log(`\nFAIL: ${differences} difference(s) across ${allNames.size} requests`);
  process.exit(1);
}

main();
