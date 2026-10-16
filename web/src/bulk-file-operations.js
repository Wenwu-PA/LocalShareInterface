import {h} from 'preact';
import {useEffect,useRef,useState} from 'preact/hooks';
import htm from 'htm';
import {bulkPlan,runBulk} from './bulk-operations.js';
import {topLevelSelection} from './file-selection.js';
const html=htm.bind(h);
export function BulkFileOperations({paths,api,onClose,onChanged,action="trash",folder="."}) {
 const ref=useRef(),alive=useRef(true),stop=useRef(false),running=useRef(false);
 const[destination,setDestination]=useState(folder),[conflict,setConflict]=useState('skip'),[error,setError]=useState('');
 const title={trash:'Выбранное в корзину',copy:'Копировать выбранное',move:'Переместить выбранное'}[action];
 const[targets]=useState(()=>topLevelSelection(paths));
 const[busy,setBusy]=useState(false),[done,setDone]=useState(0),[result,setResult]=useState(null);
 useEffect(()=>{ref.current.showModal();return()=>{alive.current=false;stop.current=true}},[]);
 async function run(){
  if(running.current)return;
  let plan;try{plan=bulkPlan(paths,action,destination,conflict)}catch(e){setError(e.message);return}
  running.current=true;setBusy(true);setError('');
  const outcome=await runBulk(plan,api,()=>stop.current,n=>{if(alive.current)setDone(n)});
  if(alive.current){setResult(outcome);setBusy(false);onChanged()}
 }
 return html`<dialog ref=${ref} class="monitor-dialog" onCancel=${e=>{if(busy)e.preventDefault();else onClose()}}>
  <div class="panel-heading"><h2>${title}</h2><button class="icon-button" aria-label="Закрыть" disabled=${busy} onClick=${onClose}>×</button></div>
  <p>Объектов: ${targets.length}. Содержимое выбранных папок обрабатывается вместе с ними.</p>
  ${action!=='trash'&&!result&&html`<label class="field"><span>Папка назначения</span><input aria-label="Папка назначения" value=${destination} disabled=${busy} onInput=${e=>setDestination(e.currentTarget.value)}/></label><p class="muted">Путь существующей папки относительно общей папки. Точка — корень. Имена объектов сохраняются.</p><label class="field"><span>При совпадении имени</span><select aria-label="Совпадение имён при групповой операции" value=${conflict} disabled=${busy} onChange=${e=>setConflict(e.currentTarget.value)}><option value="skip">Не заменять (показать конфликт)</option><option value="both">Сохранить оба</option><option value="replace">Заменить (старый объект в корзину)</option></select></label>`}
  ${error&&html`<p class="form-error" role="alert">${error}</p>`}
  ${!result&&html`<ul>${targets.slice(0,20).map(path=>html`<li class="bulk-path" key=${path}>${path}</li>`)}</ul>${targets.length>20&&html`<p class="muted">И ещё ${targets.length-20} объектов.</p>`}<p class="warning">${action==='trash'?'Корзина автоматически очищается по сроку и размеру из настроек хранилища.':'Операции выполняются последовательно. При ошибке или остановке уже выполненные действия не отменяются.'}</p>`}
  ${busy&&html`<p role="status">Обработано ${done} из ${targets.length}</p>`}
  ${result&&html`<p role="status">${action==='copy'?'Скопировано':'Перенесено'}: ${result.success}. Ошибок: ${result.failed.length}. Не обработано: ${result.pending}.</p>${result.failed.length>0&&html`<div class="bulk-errors">${result.failed.map(item=>html`<p class="form-error bulk-path" key=${item.path}>${item.path}: ${item.message}</p>`)}</div>`}`}
  <div class="dialog-actions">${busy?html`<button class="button secondary" onClick=${()=>{stop.current=true}}>Остановить после текущего</button>`:result?html`<button class="button" onClick=${onClose}>Готово</button>`:html`<button class="button secondary" onClick=${onClose}>Отмена</button><button class="button" onClick=${run}>${{trash:'Перенести в корзину',copy:'Копировать',move:'Переместить'}[action]}</button>`}</div>
 </dialog>`;
}
