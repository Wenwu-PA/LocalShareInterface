import test from 'node:test';
import assert from 'node:assert/strict';
import {bulkPlan,runBulk} from './bulk-operations.js';
test('plan excludes children and uses leaf names in destination folder',()=>{
 assert.deepEqual(bulkPlan(['a','a/b','x/c'],'copy','dest\\sub/','both'),[
  {action:'copy',source:'a',target:'dest/sub/a',conflict:'both'},
  {action:'copy',source:'x/c',target:'dest/sub/c',conflict:'both'}]);
 assert.equal(bulkPlan(['a/b'],'move','.')[0].target,'b');
 assert.deepEqual(bulkPlan(['a'],'trash'),[{action:'trash',source:'a'}]);
});
test('reject unsafe or ambiguous destination and excessive selections',()=>{
 for(const path of ['', '/', '/abs', '../x', 'a/../b', './x', 'a//b', 'C:\\x'])assert.throws(()=>bulkPlan(['a'],'copy',path));
 assert.throws(()=>bulkPlan(Array(1001).fill('a'),'copy','dest'));
});
test('permission/conflict failures continue sequentially with accurate totals',async()=>{
 const calls=[],progress=[];
 const result=await runBulk(bulkPlan(['a','b','c'],'trash'),async(_,__,op)=>{calls.push(op.source);if(op.source==='b')throw Object.assign(new Error('denied'),{status:403})},()=>false,n=>progress.push(n));
 assert.deepEqual(calls,['a','b','c']);assert.deepEqual(progress,[1,2,3]);
 assert.deepEqual(result,{success:2,failed:[{path:'b',message:'denied'}],pending:0});
});
test('server/network/login failure stops remaining requests',async()=>{
 for(const status of [undefined,401,500,507]){
  const result=await runBulk(bulkPlan(['a','b'],'trash'),async()=>{throw Object.assign(new Error('failed'),{status})},()=>false,()=>{});
  assert.equal(result.failed.length,1);assert.equal(result.pending,1);
 }
});
test('stop after current retains completed result',async()=>{
 let stop=false;
 const result=await runBulk(bulkPlan(['a','b'],'trash'),async()=>{stop=true},()=>stop,()=>{});
 assert.deepEqual(result,{success:1,failed:[],pending:1});
});
