import {h} from 'preact';
import {useEffect,useState} from 'preact/hooks';
import htm from 'htm';
import {dateStamp} from './date-display.js';
const html=htm.bind(h);
export function DeviceOverview({api}) {
 const[data,setData]=useState(null),[error,setError]=useState('');
 useEffect(()=>{
  let alive=true,busy=false;
  async function load(){
   if(document.hidden||busy)return;
   busy=true;
   try{const value=await api('devices/summary');if(alive){setData(value);setError('')}}
   catch(e){if(alive)setError(e.message)}
   finally{busy=false}
  }
  const update=e=>{if(e.detail.type==='devices'||e.detail.type==='snapshot')load()};
  load();const timer=setInterval(load,30000);
  addEventListener('lanbridge-event',update);document.addEventListener('visibilitychange',load);
  return()=>{alive=false;clearInterval(timer);removeEventListener('lanbridge-event',update);document.removeEventListener('visibilitychange',load)};
 },[api]);
 return html`<section class="stat" aria-label="Устройства сети"><h2>УСТРОЙСТВА СЕТИ</h2><strong>${data?data.online+' / '+data.total:'—'}</strong>
  <p>Ответили / всего обнаружено</p>
  ${data&&html`<p>Не отвечают: ${data.offline} · Не подтверждены: ${data.unknown} · Незнакомые: ${data.unrecognized}</p><p>Последнее сканирование: ${data.last_scan?dateStamp(data.last_scan*1000):'не выполнялось'}. ${data.enabled?'Автопоиск включён':'Автопоиск выключен'}</p>`}
  ${error?html`<p class="warning" role="status">Сводка не обновлена: ${error}</p>`:data?.error&&html`<p class="warning">Последнее сканирование завершилось с ошибкой.</p>`}
  <a href="#devices">Открыть устройства</a>
 </section>`;
}
