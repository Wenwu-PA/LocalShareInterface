import test from 'node:test';
import assert from 'node:assert/strict';
import {pdfScale} from './pdf-limits.js';
test('PDF canvas respects edge and pixel limits',()=>{
 for(const [w,h] of [[612,792],[10000,10000],[10,1e8]]){
  const scale=pdfScale(w,h,2);
  assert.ok(w*scale<=4096&&h*scale<=4096);
  assert.ok(w*scale*h*scale<=4*1024*1024+1);
 }
 assert.equal(pdfScale(612,792,.5),.5);
 for(const w of [0,-1,NaN,Infinity])assert.throws(()=>pdfScale(w,100));
});
