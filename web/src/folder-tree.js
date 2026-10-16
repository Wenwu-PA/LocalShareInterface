import {h} from 'preact';
import {useEffect,useState} from 'preact/hooks';
import htm from 'htm';
import {dragOver,dragLeave} from './file-drop.js';
const html=htm.bind(h);
export function FolderTree({api,folder,onNavigate,onDrop}) {
 const[shown,setShown]=useState(true),[revision,setRevision]=useState(0);
 return html`<aside class="folder-tree panel" aria-label="Дерево папок">
  <div class="panel-heading"><h2>Папки</h2><button class="icon-button" aria-label=${shown?'Свернуть дерево папок':'Показать дерево папок'} aria-expanded=${shown} onClick=${()=>setShown(!shown)}>${shown?'−':'+'}</button></div>
  ${shown&&html`<nav aria-label="Навигация по папкам"><button class="folder-link" onDragOver=${dragOver} onDragLeave=${dragLeave} onDrop=${e=>onDrop(e,'.')} aria-current=${folder==='.'?'location':undefined} onClick=${()=>onNavigate('.')}>Общая папка</button><${Branch} key=${revision} api=${api} path="." folder=${folder} onNavigate=${onNavigate} onDrop=${onDrop}/></nav><button class="button secondary" onClick=${()=>setRevision(revision+1)}>Обновить дерево</button>`}
 </aside>`;
}
function Branch({api,path,folder,onNavigate,onDrop,depth=0}) {
 const[items,setItems]=useState([]),[offset,setOffset]=useState(0),[more,setMore]=useState(false),[busy,setBusy]=useState(true),[error,setError]=useState(''),[expanded,setExpanded]=useState(null),[retry,setRetry]=useState(0);
 useEffect(()=>{
  let active=true;setBusy(true);setError('');setItems([]);setExpanded(null);
  api('files?'+new URLSearchParams({path,folders:1,limit:50,offset})).then(data=>{if(active){setItems(data);setMore(data.length===50)}}).catch(e=>{if(active)setError(e.message)}).finally(()=>{if(active)setBusy(false)});
  return()=>{active=false};
 },[path,offset,retry]);
 return html`<div class="folder-branch" aria-busy=${busy}>
  ${busy&&html`<p class="muted" role="status">Загрузка папок…</p>`}
  ${error&&html`<p class="form-error" role="alert">${error}</p><button class="folder-link" onClick=${()=>setRetry(retry+1)}>Повторить</button>`}
  ${!busy&&!error&&!items.length&&html`<p class="muted">Нет подпапок</p>`}
  ${items.map(item=>html`<div key=${item.path}><div class="folder-tree-row"><button class="icon-button" aria-label=${(expanded===item.path?'Свернуть ':'Раскрыть ')+item.name} aria-expanded=${expanded===item.path} onClick=${()=>setExpanded(expanded===item.path?null:item.path)}>${expanded===item.path?'−':'+'}</button><button class="folder-link" onDragOver=${dragOver} onDragLeave=${dragLeave} onDrop=${e=>onDrop(e,item.path)} title=${item.path} aria-current=${folder===item.path?'location':undefined} onClick=${()=>onNavigate(item.path)}>${item.name}</button></div>${expanded===item.path&&html`<div class=${depth<5?'folder-indent':''}><${Branch} api=${api} path=${item.path} folder=${folder} onNavigate=${onNavigate} onDrop=${onDrop} depth=${depth+1}/></div>`}</div>`)}
  ${(offset>0||more)&&!error&&html`<div class="folder-pages"><button class="folder-link" disabled=${busy||offset===0} onClick=${()=>setOffset(Math.max(0,offset-50))}>Назад</button><span>${items.length?(offset+1)+'–'+(offset+items.length):'Нет папок'}</span><button class="folder-link" disabled=${busy||!more} onClick=${()=>setOffset(offset+50)}>Далее</button></div>`}
 </div>`;
}
