#!/usr/bin/env node
// This checks only parameter-free routes and tools, not endpoint coverage.
import { readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { sha256hex, fixedGetRoutes } from './lib.mjs';

export function hasPathParameter(routePath) {
  return routePath.replaceAll('{$}', '').includes('{');
}
export function routeMatches(route, request) {
  if (route.method !== 'ANY' && route.method !== request.method && !(route.method === 'GET' && request.method === 'HEAD')) return false;
  const pattern = route.path.replaceAll('{$}', '').split(/(\{[^}]+\})/).map(part => part.startsWith('{') ? (part.includes('...') ? '.*' : '[^/]+') : part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('');
  const subtree = route.path.endsWith('/') && !route.path.endsWith('{$}');
  return new RegExp(`^${pattern}${subtree ? '.*' : ''}$`).test(request.path.split('?')[0]);
}
export function touchedTools(requests) {
  const result = new Set();
  for (const request of requests) {
    const match = /^tools\/call ([\w-]+)(?: ([\w-]+))?/.exec(request.name);
    if (!match) continue;
    result.add(match[1]);
    // run.mjs names wrapper calls "tools/call run_tool <target>".
    const resultBody = request.body?.result;
    const text = (resultBody?.content || []).map(c => c.text || '').join('\n');
    const wrapperRefused = /Unknown tool:|Nested discovery cannot call|passcode_screen is only for|arguments must be an object|unexpected argument [^\n]*for run_tool|^name (?:is required|must be)/i.test(text);
    if (match[1] === 'run_tool' && match[2] && resultBody && !request.body?.error && !wrapperRefused) result.add(match[2]);
  }
  return result;
}
export function coverageReport(inventory, recordings) {
  const items = new Set(), parameterized = [];
  for (const product of [...new Set(recordings.map(r => r.product))].sort()) {
    const requests = recordings.filter(r => r.product === product).flatMap(r => r.requests);
    const routes = inventory.routes.filter(r => !r.edition || r.edition === product);
    const withParams = new Map();
    for (const route of routes) {
      const touched = requests.some(r => routeMatches(route, r));
      if (hasPathParameter(route.path)) withParams.set(`${route.method} ${route.path}`, touched || withParams.get(`${route.method} ${route.path}`));
      else if (!touched) items.add(`route ${product} ${route.method} ${route.path}`);
    }
    parameterized.push({ product, touched: [...withParams.values()].filter(Boolean).length, untouched: [...withParams.values()].filter(v => !v).length });
    const tools = touchedTools(requests);
    for (const tool of inventory.tools) {
      if (tool.edition !== product && !(tool.edition === 'screen' && product === 'host')) continue;
      if (!tools.has(tool.name)) items.add(`tool ${product} ${tool.name}`);
    }
  }
  return { items: [...items].sort(), parameterized };
}
export function inventoryItems(inventory, product) {
  return [
    ...inventory.routes.filter(r => (!r.edition || r.edition === product) && !hasPathParameter(r.path)).map(r => `route ${product} ${r.method} ${r.path}`),
    ...inventory.tools.filter(t => t.edition === product || (t.edition === 'screen' && product === 'host')).map(t => `tool ${product} ${t.name}`),
  ];
}
export function coveredItems(inventory, recordings) {
  const missing = new Set(coverageReport(inventory, recordings).items);
  const automatic = automaticItems(inventory);
  return [...new Set(recordings.flatMap(r => inventoryItems(inventory, r.product)))].filter(item => !missing.has(item) && !automatic.has(item)).sort();
}
export function automaticItems(inventory) {
  const fixed = fixedGetRoutes(inventory);
  return new Set(['host','hack'].flatMap(product => inventory.routes.filter(r => (!r.edition || r.edition === product) && r.method === 'GET' && !hasPathParameter(r.path) && fixed.has(r.path.replaceAll('{$}', ''))).map(r => `route ${product} ${r.method} ${r.path}`)));
}
export function checkPartition(inventory, coveredText, exceptionsText) {
  const covered = coveredText.split('\n').filter(l => l.trim() && !l.startsWith('#'));
  const exceptions = exceptionsText.split('\n').filter(l => l.trim() && !l.startsWith('#')).map(l => l.split(' | ')[0]);
  const automatic = automaticItems(inventory);
  const all = new Set(['host','hack'].flatMap(p => inventoryItems(inventory,p)));
  const errors = [];
  for (const item of [...covered, ...exceptions]) if (!all.has(item)) errors.push(`unknown inventory item: ${item}`);
  for (const item of all) {
    const count = Number(automatic.has(item)) + covered.filter(x => x === item).length + exceptions.filter(x => x === item).length;
    if (count !== 1) errors.push(`inventory partition (${count}): ${item}`);
  }
  errors.push(...compareExceptions(exceptions, exceptionsText));
  return errors.sort();
}

export function compareExceptions(items, text) {
  const exceptions = new Map(), errors = [];
  for (const line of text.split('\n').filter(l => l.trim() && !l.startsWith('#'))) {
    const split = line.indexOf(' | ');
    const item = (split < 0 ? line : line.slice(0, split)).trim();
    if (split < 0 || !line.slice(split + 3).trim()) errors.push(`missing reason: ${item}`);
    if (exceptions.has(item)) errors.push(`duplicate exception: ${item}`);
    exceptions.set(item, true);
  }
  const expected = new Set(items);
  for (const item of items) if (!exceptions.has(item)) errors.push(`missing exception: ${item}`);
  for (const item of exceptions.keys()) if (!expected.has(item)) errors.push(`stale exception: ${item}`);
  return errors.sort();
}
function main() {
  const args = process.argv.slice(2), print = args.includes('--print');
  const writeCovered = args.includes('--write-covered');
  const folder = args.filter(a => !['--print','--write-covered'].includes(a));
  if (folder.length !== 1) { console.error('usage: node scripts/golden/coverage.mjs DIR [--print] [--write-covered]'); process.exitCode = 2; return; }
  const here = path.dirname(fileURLToPath(import.meta.url));
  const manifest = JSON.parse(readFileSync(path.join(folder[0], 'manifest.json')));
  const recordings = manifest.runs.map(run => JSON.parse(readFileSync(path.join(folder[0], run.file))));
  for (const recording of recordings) for (const request of recording.requests) {
    if (!request.text) continue;
    const bytes = readFileSync(path.join(folder[0], request.text.file));
    if (bytes.length !== request.text.length || sha256hex(bytes) !== request.text.sha256) throw new Error(`text integrity error: ${request.text.file}`);
  }
  const inventory = JSON.parse(readFileSync(path.join(here, '../../docs/generated/inventory.json')));
  const report = coverageReport(inventory, recordings);
  console.error('Checked list: routes without a path parameter plus tools. This is not endpoint coverage.');
  for (const info of report.parameterized) console.error(`${info.product}: routes with path parameters: ${info.touched} touched, ${info.untouched} untouched (information only)`);
  if (print) { console.log(report.items.map(item => `${item} | `).join('\n')); return; }
  const errors = compareExceptions(report.items, readFileSync(path.join(here, 'coverage-exceptions.txt'), 'utf8'));
  const touched = coveredItems(inventory, recordings).join('\n') + '\n';
  const coveredFile = path.join(here, 'coverage-covered.txt');
  if (writeCovered) writeFileSync(coveredFile, touched);
  else if (readFileSync(coveredFile, 'utf8') !== touched) errors.push('touched list differs from coverage-covered.txt; review and use --write-covered');
  errors.push(...checkPartition(inventory, touched, readFileSync(path.join(here, 'coverage-exceptions.txt'), 'utf8')));
  if (errors.length) { console.error(errors.join('\n')); process.exitCode = 1; }
  else console.log(`ok: ${report.items.length} explained exceptions`);
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { main(); } catch (err) { console.error(err.message); process.exitCode = 1; }
}
