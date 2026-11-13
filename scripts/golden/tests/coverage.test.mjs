import test from 'node:test';
import assert from 'node:assert/strict';
import { coverageReport, compareExceptions, routeMatches, touchedTools } from '../coverage.mjs';
const inventory = { routes: [{ method: 'GET', path: '/covered' }, { method: 'ANY', path: '/missing' }, { method: 'GET', path: '/sites/{site}' }], tools: [{ name: 'list_sites', edition: 'host' }] };
const recordings = [{ product: 'host', requests: [{ name: 'GET /covered', method: 'GET', path: '/covered?x=1' }, { name: 'site-read', method: 'GET', path: '/sites/one' }, { name: 'tools/call run_tool list_sites', method: 'POST', path: '/mcp', body: { result: { isError: false } } }] }];
test('covered and uncovered routes, ANY methods and parameter information', () => {
  const report = coverageReport(inventory, recordings);
  assert.deepEqual(report.items, ['route host ANY /missing']);
  assert.deepEqual(report.parameterized, [{ product: 'host', touched: 1, untouched: 0 }]);
  assert.ok(routeMatches({ method: 'GET', path: '/{$}' }, { method: 'GET', path: '/' }));
  assert.ok(routeMatches({ method: 'ANY', path: '/covered' }, { method: 'POST', path: '/covered?q=2' }));
});
test('stale exception is rejected', () => {
  assert.deepEqual(compareExceptions([], 'route host GET /covered | Now covered.\n'), ['stale exception: route host GET /covered']);
});
test('missing reasons and missing exceptions fail', () => {
  assert.deepEqual(compareExceptions(['tool host list_sites'], 'tool host list_sites | \n'), ['missing reason: tool host list_sites']);
  assert.deepEqual(compareExceptions(['tool host list_sites'], ''), ['missing exception: tool host list_sites']);
});
test('run_tool touches its named target; listing alone does not', () => {
  assert.ok(!coverageReport(inventory, recordings).items.includes('tool host list_sites'));
  assert.ok(coverageReport(inventory, [{ product: 'host', requests: [{ name: 'tools/list personal', method: 'POST', path: '/mcp' }] }]).items.includes('tool host list_sites'));
});

test('a refused wrapper records dispatch but does not cover its target', () => {
  const report = coverageReport(inventory, [{ product: 'host', requests: [{
    name: 'tools/call run_tool list_sites', method: 'POST', path: '/mcp',
    body: { error: { message: 'unknown tool: run_tool' } },
  }] }]);
  assert.ok(report.items.includes('tool host list_sites'));
});

import { readFileSync } from 'node:fs';
import { checkPartition } from '../coverage.mjs';
test('real generated inventory is partitioned by automatic, covered, exceptions', () => {
  const read = relative => readFileSync(new URL(relative, import.meta.url), 'utf8');
  assert.deepEqual(checkPartition(JSON.parse(read('../../../docs/generated/inventory.json')),read('../coverage-covered.txt'),read('../coverage-exceptions.txt')),[]);
});
test('ServeMux HEAD, subtree and exact end marker', () => {
  assert.ok(routeMatches({method:'GET',path:'/docs/'},{method:'HEAD',path:'/docs/page'}));
  assert.ok(!routeMatches({method:'GET',path:'/docs/{$}'},{method:'GET',path:'/docs/page'}));
});

test('wrapper dispatch includes omitted isError and target errors, excludes wrapper refusals',()=>{
  const run=result=>touchedTools([{name:'tools/call run_tool list_sites',body:{result}}]).has('list_sites');
  assert.ok(run({content:[]}));assert.ok(run({isError:true,content:[{text:'site not found [site_not_found]'}]}));
  assert.ok(!run({isError:true,content:[{text:'Unknown tool: run_tool'}]}));
  assert.ok(!run({isError:true,content:[{text:'Nested discovery cannot call another discovery tool.'}]}));
});

import { mcpCallName } from '../lib.mjs';
test('generic wrapper helper keeps its target ahead of a qualifier for coverage', () => {
  const name = mcpCallName('tools/call run_tool extra', {name:'run_tool',arguments:{name:'list_sites',arguments:{}}});
  assert.ok(touchedTools([{name,body:{result:{content:[]}}}]).has('list_sites'));
  assert.ok(!touchedTools([{name,body:{result:{content:[]}}}]).has('extra'));
});
