import {h} from 'preact';
import {useEffect,useRef,useState} from 'preact/hooks';
import htm from 'htm';
import {topLevelSelection} from './file-selection.js';
const html=htm.bind(h);
export function BulkTrash({paths,api,onClose,onChanged}) {
 const ref=useRef(),alive=useRef(true),stop=useRef(false),running=useRef(false);
 const[targets]=useState(()=>topLevelSelection(paths));
 const[busy,setBusy]=useState(false),[done,setDone]=useState(0),[result,setResult]=useState(null);
 useEffect(()=>{ref.current.showModal();return()=>{alive.current=false;stop.current=true}},[]);
 async function run(){
  if(running.current)return;
  running.current=true;setBusy(true);const failed=[];let success=0,processed=0;
  for(const path of targets){
   if(stop.current)break;
   try{await api('files/operation','POST',{action:'trash',source:path});success++}
   catch(e){failed.push({path,message:e.message});if(!e.status||e.status>=500||e.status===401)stop.current=true}
   processed++;if(alive.current)setDone(processed);
  }
  if(alive.current){setResult({success,failed,pending:targets.length-processed});setBusy(false);onChanged()}
 }
 return html`<dialog ref=${ref} class="monitor-dialog" onCancel=${e=>{if(busy)e.preventDefault();else onClose()}}>
  <div class="panel-heading"><h2>Выбранное в корзину</h2><button class="icon-button" aria-label="Закрыть" disabled=${busy} onClick=${onClose}>×</button></div>
  <p>Объектов: ${targets.length}. Содержимое выбранных папок переносится вместе с ними.</p>
  ${!result&&html`<ul>${targets.slice(0,20).map(path=>html`<li class="bulk-path" key=${path}>${path}</li>`)}</ul>${targets.length>20&&html`<p class="muted">И ещё ${targets.length-20} объектов.</p>`}<p class="warning">Корзина автоматически очищается по сроку и размеру из настроек хранилища.</p>`}
  ${busy&&html`<p role="status">Обработано ${done} из ${targets.length}</p>`}
  ${result&&html`<p role="status">Перенесено: ${result.success}. Ошибок: ${result.failed.length}. Не обработано: ${result.pending}.</p>${result.failed.length>0&&html`<div class="bulk-errors">${result.failed.map(item=>html`<p class="form-error bulk-path" key=${item.path}>${item.path}: ${item.message}</p>`)}</div>`}`}
  <div class="dialog-actions">${busy?html`<button class="button secondary" onClick=${()=>{stop.current=true}}>Остановить после текущего</button>`:result?html`<button class="button" onClick=${onClose}>Готово</button>`:html`<button class="button secondary" onClick=${onClose}>Отмена</button><button class="button" onClick=${run}>Перенести в корзину</button>`}</div>
 </dialog>`;
}
