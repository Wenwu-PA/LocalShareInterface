import {createContext,h} from 'preact';
import {useEffect,useState} from 'preact/hooks';
import htm from 'htm';
const html=htm.bind(h);
export const Branding=createContext('');
export function useBranding(api,authenticated){
 const[logo,setLogo]=useState('');
 useEffect(()=>{
  let alive=true,version=0;
  if(!authenticated){setLogo('');return}
  const load=async()=>{const current=++version;try{const value=await api('branding');if(alive&&current===version)setLogo(value.logo||'')}catch{}};
  const update=e=>{if(['branding','snapshot'].includes(e.detail.type))load()};
  load();addEventListener('lanbridge-event',update);
  return()=>{alive=false;removeEventListener('lanbridge-event',update)};
 },[authenticated,api]);
 return logo;
}
export function LogoSettings({logo,csrf,notify,disabled}){
 const[busy,setBusy]=useState(false);
 async function save(file){
  if(file&&(file.size>256*1024||!['image/png','image/jpeg'].includes(file.type))){notify('Выберите PNG/JPEG до 256 КиБ',true);return}
  setBusy(true);
  try{
   const response=await fetch('/api/branding',{method:file?'PUT':'DELETE',credentials:'same-origin',headers:{'X-CSRF-Token':csrf,...(file?{'Content-Type':file.type}:{})},body:file||undefined});
   const value=await response.json();if(!response.ok)throw new Error(value.error||'Не удалось сохранить логотип');
   dispatchEvent(new CustomEvent('lanbridge-event',{detail:{type:'branding'}}));
   notify(file?'Логотип сохранён':'Логотип удалён');
  }catch(e){notify(e.message,true)}finally{setBusy(false)}
 }
 return html`<fieldset class="logo-settings" disabled=${disabled||busy}><legend>Логотип сети</legend>${logo&&html`<img src=${logo} alt="Текущий логотип сети" width="64" height="64"/>`}<label class="field"><span>Загрузить логотип</span><input type="file" accept="image/png,image/jpeg" onChange=${e=>{const file=e.currentTarget.files?.[0];e.currentTarget.value='';if(file)save(file)}}/></label><p class="muted">PNG/JPEG до 256 КиБ и 512 × 512. Применяется сразу после загрузки.</p><button type="button" class="button secondary" disabled=${!logo||busy} onClick=${()=>save(null)}>${busy?'Сохранение…':'Удалить логотип'}</button></fieldset>`;
}
