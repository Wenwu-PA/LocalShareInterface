import {h} from 'preact';
import {useEffect,useState} from 'preact/hooks';
import htm from 'htm';
const html=htm.bind(h);
const bytes=value=>value>=1073741824?(value/1073741824).toFixed(1)+' ГиБ':(value/1048576).toFixed(1)+' МиБ';

export function Resources({api,details=false}) {
 const[data,setData]=useState(null),[error,setError]=useState('');
 useEffect(()=>{
  let alive=true,busy=false;
  const load=async()=>{
   if(busy||document.hidden)return;
   busy=true;
   try{const next=await api('resources');if(alive){setData(next);setError('')}}
   catch(e){if(alive)setError('Не удалось обновить состояние ресурсов: '+e.message)}
   finally{busy=false}
  };
  load();const timer=setInterval(load,30000);document.addEventListener('visibilitychange',load);
  return()=>{alive=false;clearInterval(timer);document.removeEventListener('visibilitychange',load)};
 },[api]);
 const unknown=code=>data?.alerts.some(a=>a.code===code&&a.level==='unknown');
 return html`<section class="resource-status" aria-label="Ресурсы сервера">
  ${error&&html`<p class="connection-note" role="status">${error}${data?' Показаны предыдущие измерения.':''}</p>`}
  ${data?.alerts.map(a=>html`<p key=${a.code} class=${a.level==='critical'?'form-error':'connection-note'} role="status"><strong>${a.level==='critical'?'Критично: ':a.level==='unknown'?'Нет измерения: ':'Внимание: '}</strong>${a.message}</p>`)}
  ${details&&data&&html`<div class="stats three">
   <section class="stat"><h2>БАЗА ДАННЫХ</h2><strong>${bytes(data.database_bytes)}</strong><p>Лимит ${bytes(data.database_limit)} · свободных страниц ${bytes(data.reusable_bytes)}</p><p>WAL: ${bytes(data.wal_bytes)}</p></section>
   <section class="stat"><h2>СВОБОДНО НА ДИСКЕ</h2><strong>${unknown('storage_disk')?'Нет данных':bytes(data.storage_available)}</strong><p>Хранилище · диск БД: ${unknown('data_disk')?'нет данных':bytes(data.data_available)}</p></section>
   <section class="stat"><h2>ПАМЯТЬ GO</h2><strong>${bytes(data.heap_bytes)}</strong><p>Занятая куча · ${data.goroutines} горутин</p><p>Не включает всю память процесса и ОС</p></section>
  </div>`}
 </section>`;
}
