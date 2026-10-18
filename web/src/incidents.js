import {t} from './i18n/index.js';
import {dateStamp} from './date-display.js';
import {h} from 'preact';
import {useEffect,useRef,useState} from 'preact/hooks';
import htm from 'htm';
const html=htm.bind(h);
export function IncidentList({api,notify,user}){
 const[list,setList]=useState([]),[selected,setSelected]=useState(null),[filter,setFilter]=useState('');useEffect(()=>{const load=()=>api('incidents').then(setList).catch(e=>notify(e.message,true));load();const timer=setInterval(()=>{if(!document.hidden)load()},10000);return()=>clearInterval(timer)},[]);
 const visible=list.filter(item=>!filter||(filter==='open'?!item.closed:!!item.closed));
 return html`<section class="panel"><div class="panel-heading"><h2>${t('incidents.history')}</h2>${user.role==='admin'&&html`<a class="button secondary" href="/api/incidents/export" download>${t('incidents.export')}</a>`}</div><label class="field"><span>${t('incidents.filter')}</span><select value=${filter} onChange=${e=>setFilter(e.currentTarget.value)}><option value="">${t('incidents.all')}</option><option value="open">${t('incidents.open')}</option><option value="closed">${t('incidents.closed')}</option></select></label>${visible.length?html`<div class="table-scroll"><table><thead><tr><th>${t('incidents.columnEvent')}</th><th>${t('incidents.columnStart')}</th><th>${t('incidents.columnDuration')}</th><th>${t('incidents.columnState')}</th></tr></thead><tbody>${visible.map(item=>html`<tr key=${item.id}><td><button class="monitor-name" onClick=${()=>setSelected(item)}>${item.number} · ${item.name}</button><p class="muted">${item.message}</p></td><td>${dateStamp(item.opened*1000)}</td><td>${t('incidents.durationSeconds',{seconds:Math.max(0,(item.closed||Math.floor(Date.now()/1000))-item.opened)})}</td><td class=${'monitor-status '+(item.closed?'up':'down')}>${item.closed?t('incidents.recovered'):t('incidents.down')}</td></tr>`)}</tbody></table></div>`:html`<div class="empty"><h3>${t('incidents.empty')}</h3><p>${t('incidents.emptyHint')}</p></div>`}</section>${selected&&html`<${IncidentDialog} item=${selected} api=${api} notify=${notify} user=${user} onClose=${()=>setSelected(null)}/>`}`;
}
function IncidentDialog({item,api,notify,user,onClose}){
 const[events,setEvents]=useState([]),[note,setNote]=useState(''),[busy,setBusy]=useState(false);const ref=useRef();
 const load=()=>api('incidents/'+item.id+'/events').then(setEvents).catch(e=>notify(e.message,true));
 useEffect(()=>{ref.current.showModal();load()},[]);
 async function save(e){e.preventDefault();setBusy(true);try{await api('incidents/'+item.id+'/notes','POST',{message:note});setNote('');await load()}catch(e){notify(e.message,true)}finally{setBusy(false)}}
 return html`<dialog ref=${ref} onCancel=${onClose}><header><h2>${item.number} · ${item.name}</h2><button class="icon-button" aria-label=${t('incidents.close')} onClick=${onClose}>×</button></header>${events.map((event,index)=>html`<div class="notification-channel" key=${index}><small class="muted">${dateStamp(event.at*1000)} · ${{opened:t('incidents.down'),recovered:t('incidents.recovery'),note:t('incidents.note')}[event.kind]||event.kind}</small><p>${event.message}</p></div>`)}${user.role==='admin'&&html`<form onSubmit=${save}><label class="field"><span>${t('incidents.note')}</span><textarea required maxLength="1000" rows="3" value=${note} onInput=${e=>setNote(e.currentTarget.value)}/></label><button class="button" disabled=${busy}>${t('incidents.addNote')}</button></form>`}</dialog>`;
}
