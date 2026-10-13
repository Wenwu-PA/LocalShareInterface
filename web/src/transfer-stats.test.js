import {test} from 'node:test';
import assert from 'node:assert/strict';
import {transferStats} from './transfer-stats.js';
test('elapsed duration and mean rate',()=>{assert.deepEqual(transferStats({started:100,finished:110,size:10240}),{duration:'10 с',speed:1024})});
test('subsecond records have no invented rate',()=>{assert.deepEqual(transferStats({started:100,finished:100,size:10240}),{duration:'менее 1 с',speed:null})});
test('invalid timestamps and clock rollback',()=>{for(const finished of [99,NaN,Infinity,undefined])assert.deepEqual(transferStats({started:100,finished,size:10}),{duration:'—',speed:null})});
test('long duration and empty file',()=>{assert.deepEqual(transferStats({started:100,finished:3761,size:0}),{duration:'1 ч 1 мин',speed:0})});
test('negative size has no rate',()=>{assert.equal(transferStats({started:100,finished:110,size:-1}).speed,null)});
