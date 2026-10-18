import test from 'node:test';
import assert from 'node:assert/strict';
import {latencyGeometry} from './latency-geometry.js';
test('small latency series retains coordinates and endpoints',()=>{
 const chart=latencyGeometry([{at:1,latency:10},{at:2,latency:20},{at:3,latency:0}],100);
 assert.equal(chart.line,'0,95 320,20 640,170');
 assert.equal(chart.max,20);assert.equal(chart.count,3);
 assert.equal(chart.first.at,1);assert.equal(chart.last.at,3);
});
test('dense series is bounded by width and preserves spikes and troughs',()=>{
 const points=Array.from({length:10000},(_,i)=>({at:i,latency:50}));
 points[1234].latency=1000;points[5678].latency=0;
 const chart=latencyGeometry(points,100);
 const xy=chart.line.split(' ').map(pair=>pair.split(',').map(Number));
 assert.ok(chart.count<=202);assert.equal(chart.max,1000);
 assert.ok(xy.some(([x,y])=>x===1234/9999*640&&y===20));
 assert.ok(xy.some(([x,y])=>x===5678/9999*640&&y===170));
 assert.equal(xy[0][0],0);assert.equal(xy.at(-1)[0],640);
 assert.ok(xy.every(([x],i)=>i===0||x>xy[i-1][0]));
 assert.equal(points.length,10000);
 assert.ok(latencyGeometry(points,1).count<=4);
 assert.ok(latencyGeometry(points,99999).count<=1282);
});
test('invalid points and widths cannot produce invalid SVG coordinates',()=>{
 assert.equal(latencyGeometry(null).line,'');
 const points=[null,{at:1,latency:NaN},{at:2,latency:-1},{at:Infinity,latency:3},{at:4,latency:5}];
 for(const width of [NaN,Infinity,0,-10]){
  const chart=latencyGeometry(points,width);
  assert.equal(chart.count,1);assert.equal(chart.line,'0,95');
 }
});
