import {t} from './i18n/index.js';
import {dateStamp} from './date-display.js';
import {h} from 'preact';
import {useEffect,useRef,useState} from 'preact/hooks';
import htm from 'htm';
const html=htm.bind(h);
const actions=Object.fromEntries(["notification_test", "incident_note", "branding", "discovery_config", "device_updated", "setup", "login", "login_failed", "user_created", "account_updated", "settings", "file_access", "storage_settings", "monitor_saved", "monitor_bulk", "notification_channel", "external_heartbeat", "network_scan", "share.create", "share.revoke", "external.start", "external.stop", "database_recovered"].map(id=>[id,t('audit.'+id)]));

export function AuditHistory({api}){
 const[open,setOpen]=useState(false);
 return html`<section class="panel"><details onToggle=${e=>setOpen(e.currentTarget.open)}><summary>Журнал аудита</summary>${open&&html`<${AuditPages} api=${api}/>`}</details></section>`;
}

function AuditPages({api}){
 const[filter,setFilter]=useState({action:'',user_id:''}),[before,setBefore]=useState(0),[previous,setPrevious]=useState([]),[revision,setRevision]=useState(0);
 const[data,setData]=useState(null),[busy,setBusy]=useState(true),[error,setError]=useState('');
 const request=useRef(0);
 useEffect(()=>{
  const id=++request.current;setBusy(true);setError('');setData(null);
  const q=new URLSearchParams({...filter,limit:50});if(before)q.set('before',before);
  api('audit?'+q).then(result=>{if(request.current===id)setData(result)}).catch(e=>{if(request.current===id)setError(e.message)}).finally(()=>{if(request.current===id)setBusy(false)});
  return()=>{request.current++};
 },[api,filter,before,revision]);
 function apply(event){event.preventDefault();if(busy)return;const f=new FormData(event.currentTarget);setFilter({action:f.get('action'),user_id:f.get('user_id')});setBefore(0);setPrevious([])}
 return html`<p class="muted">По 50 событий, от новых к старым. Хранение до года. Передачи файлов находятся в отдельной истории.</p>
  <form class="dialog-actions" onSubmit=${apply}>
   <label class="field"><span>Событие аудита</span><select name="action" aria-label="Событие аудита"><option value="">Все события</option>${Object.entries(actions).map(([value,label])=>html`<option value=${value}>${label}</option>`)}</select></label>
   <label class="field"><span>ID пользователя</span><input name="user_id" type="number" min="1" step="1" placeholder="Все"/></label>
   <button class="button secondary" disabled=${busy}>Применить фильтры</button>
   <button type="button" class="button secondary" disabled=${busy} onClick=${()=>{setBefore(0);setPrevious([]);setRevision(revision+1)}}>Обновить журнал</button>
  </form>
  ${busy&&html`<p role="status">Загрузка журнала…</p>`}${error&&html`<p class="form-error" role="alert">${error}</p>`}
  ${data&&(data.items.length?html`<div class="table-scroll"><table><thead><tr><th>ВРЕМЯ</th><th>ПОЛЬЗОВАТЕЛЬ</th><th>ДЕЙСТВИЕ</th><th>ДЕТАЛИ</th></tr></thead><tbody>${data.items.map(item=>html`<tr key=${item.id}><td>${dateStamp(item.at*1000)}</td><td>${item.username|| (item.user_id===null?'Система / без входа':'Удалённый пользователь')} ${item.user_id!==null?'#'+item.user_id:''}</td><td>${actions[item.action]||item.action}</td><td class="audit-detail">${item.detail}</td></tr>`)}</tbody></table></div>`:html`<p class="muted">Событий по этому фильтру нет.</p>`)}
  <div class="dialog-actions"><button class="button secondary" disabled=${busy||!previous.length} onClick=${()=>{setBefore(previous.at(-1));setPrevious(previous.slice(0,-1))}}>Новее</button><span class="muted">Страница ${previous.length+1}</span><button class="button secondary" disabled=${busy||!data?.has_more} onClick=${()=>{setPrevious([...previous,before]);setBefore(data.next_before)}}>Старее</button></div>`;
}
