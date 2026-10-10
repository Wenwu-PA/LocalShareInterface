import {h} from 'preact';
import {useEffect,useRef,useState} from 'preact/hooks';
import htm from 'htm';
const html=htm.bind(h);
export function Diagnostics({csrf,user}){
 const[kind,setKind]=useState('ping'),[target,setTarget]=useState(''),[lines,setLines]=useState([]),[busy,setBusy]=useState(false);const active=useRef();
 useEffect(()=>()=>active.current?.abort(),[]);
 async function run(e){e.preventDefault();const controller=new AbortController();active.current=controller;setBusy(true);setLines([]);const started=performance.now();try{
  const speed=kind==='speed';const response=await fetch(speed?'/api/diagnostics/speed':'/api/diagnostics',{method:speed?'GET':'POST',headers:speed?{}:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:speed?undefined:JSON.stringify({kind,target}),signal:controller.signal});
  if(!response.ok){const body=await response.json();throw new Error(body.error||'Ошибка диагностики')}
  const reader=response.body.getReader(),decoder=new TextDecoder();let buffered='',bytes=0;
  for(;;){const{done,value}=await reader.read();if(done)break;if(speed){bytes+=value.byteLength;continue}buffered+=decoder.decode(value,{stream:true});const records=buffered.split('\n');buffered=records.pop();for(const raw of records){if(!raw)continue;const item=JSON.parse(raw);if(item.kind!=='done')setLines(previous=>[...previous.slice(-127),item.message])}}
  if(speed){const seconds=(performance.now()-started)/1000;setLines([`${(bytes/1048576).toFixed(1)} МиБ за ${seconds.toFixed(2)} с · ${(bytes*8/seconds/1000000).toFixed(1)} Мбит/с (сервер → браузер)`])}
 }catch(e){setLines(previous=>[...previous,e.name==='AbortError'?'Отменено':e.message])}finally{setBusy(false);active.current=null}}
 return html`<section class="panel"><h2>Диагностика</h2><form onSubmit=${run}><div class="field-pair"><label class="field"><span>Проверка</span><select aria-label="Проверка" value=${kind} disabled=${busy} onChange=${e=>setKind(e.currentTarget.value)}>${[['ping','Ping'],['traceroute','Traceroute'],['dns','DNS'],['port','TCP-порт'],['speed','Скорость LAN']].map(([key,label])=>html`<option value=${key}>${label}</option>`)}</select></label>${kind!=='speed'&&html`<label class="field"><span>Цель диагностики</span><input value=${target} required placeholder=${kind==='port'?'192.168.1.1:443':'192.168.1.1'} disabled=${busy} onInput=${e=>setTarget(e.currentTarget.value)}/></label>`}</div><p class="muted">${kind==='speed'?'Загрузка 16 МиБ с сервера в браузер. На этом же компьютере измеряется loopback, не пропускная способность Wi-Fi.':'Проверка выполняется с сервера. До 45 секунд, результат появляется по мере готовности.'}</p><div class="dialog-actions"><button class="button secondary" disabled=${busy||user.role!=='admin'}>Запустить диагностику</button>${busy&&html`<button type="button" class="button secondary" onClick=${()=>active.current?.abort()}>Отменить диагностику</button>`}</div></form>${lines.length>0&&html`<pre class="diagnostic-output" role="log" aria-live="polite">${lines.join('\n')}</pre>`}</section>`;
}
