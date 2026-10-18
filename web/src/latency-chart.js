import {h} from 'preact';
import {useEffect,useMemo,useRef,useState} from 'preact/hooks';
import htm from 'htm';
import {dateStamp} from './date-display.js';
import {latencyGeometry} from './latency-geometry.js';
const html=htm.bind(h);
export function LatencyChart({points}){
 const node=useRef(),[width,setWidth]=useState(640);
 useEffect(()=>{
  let frame=0;
  const measure=()=>{
   if(document.hidden)return;
   cancelAnimationFrame(frame);
   frame=requestAnimationFrame(()=>{if(node.current&&!document.hidden)setWidth(node.current.clientWidth)});
  };
  const observer=new ResizeObserver(measure);observer.observe(node.current);measure();
  document.addEventListener('visibilitychange',measure);
  return()=>{cancelAnimationFrame(frame);observer.disconnect();document.removeEventListener('visibilitychange',measure)};
 },[]);
 const chart=useMemo(()=>latencyGeometry(points,width),[points,width]);
 return html`<div class="latency-chart" ref=${node}><small>${Math.ceil(chart.max)} мс</small><svg viewBox="0 0 640 190" role="img" aria-label="Средняя задержка проверок"><path d="M0 20H640 M0 70H640 M0 120H640 M0 170H640" stroke="var(--line)" fill="none"/><polyline points=${chart.line} stroke="var(--accent)" stroke-width="2" fill="none"/></svg><div class="chart-labels"><span>${chart.first?dateStamp(chart.first.at*1000,'time'):'—'}</span><span>${chart.last?dateStamp(chart.last.at*1000,'time'):'—'}</span></div></div>`;
}
