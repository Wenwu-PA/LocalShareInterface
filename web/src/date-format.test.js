import test from 'node:test';
import assert from 'node:assert/strict';
import {formatDate} from './date-format.js';
const instant=Date.UTC(2026,9,15,22,30,5);
test('configured timezone crosses midnight independently of browser zone',()=>{
 assert.equal(formatDate(instant,{timezone:'Europe/Moscow'}),'16.10.2026, 01:30:05');
 assert.equal(formatDate(instant,{timezone:'UTC',date_format:'iso'}),'2026-10-15, 22:30:05');
});
test('date-only, time-only, epoch, invalid values and timezone fallback',()=>{
 assert.equal(formatDate(instant,{timezone:'UTC'},'date'),'15.10.2026');
 assert.equal(formatDate(instant,{timezone:'UTC'},'time'),'22:30:05');
 assert.equal(formatDate(0,{timezone:'UTC'}),'01.01.1970, 00:00:00');
 assert.equal(formatDate(instant,{timezone:'not/a/zone'}),'15.10.2026, 22:30:05');
 for(const value of [null,undefined,NaN,Infinity,'wrong'])assert.equal(formatDate(value),'—');
});
test('daylight saving follows timezone rules',()=>{
 assert.equal(formatDate(Date.UTC(2026,2,29,0,30),{timezone:'Europe/Berlin'},'time'),'01:30:00');
 assert.equal(formatDate(Date.UTC(2026,2,29,1,30),{timezone:'Europe/Berlin'},'time'),'03:30:00');
});
