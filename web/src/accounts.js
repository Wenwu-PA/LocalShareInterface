import {h} from 'preact';
import {useEffect,useRef,useState} from 'preact/hooks';
import htm from 'htm';
const html=htm.bind(h);
export function AccountDialog({target,user,api,onClose,onSaved}){
 const ref=useRef(),[busy,setBusy]=useState(false),[error,setError]=useState(''),[role,setRole]=useState(target.role);
 useEffect(()=>{ref.current.showModal()},[]);
 async function submit(event){
  event.preventDefault();if(busy)return;
  const form=new FormData(event.currentTarget),body={current_password:form.get('current_password')};
  const password=form.get('password');
  if(password){if(password!==form.get('confirm')){setError('Пароли не совпадают');return}body.password=password}
  if(user.role==='admin'&&form.get('role')!==target.role)body.role=form.get('role');
  if(!body.password&&!body.role){setError('Укажите новый пароль или измените роль');return}
  setBusy(true);setError('');
  try{onSaved(await api('users/'+target.id,'PATCH',body))}catch(e){setError(e.message)}finally{setBusy(false)}
 }
 return html`<dialog ref=${ref} onCancel=${e=>{if(busy)e.preventDefault();else onClose()}}>
  <header><h2>Учётная запись: ${target.username}</h2><button class="icon-button" disabled=${busy} onClick=${onClose} aria-label="Закрыть">×</button></header>
  <form onSubmit=${submit}>
   <label class="field"><span>Ваш текущий пароль</span><input name="current_password" type="password" autoComplete="current-password" required maxLength="256"/></label>
   <label class="field"><span>Новый пароль пользователя</span><input name="password" type="password" autoComplete="new-password" minLength="12" maxLength="256"/><small>Оставьте пустым, чтобы изменить только роль.</small></label>
   <label class="field"><span>Повторите новый пароль</span><input name="confirm" type="password" autoComplete="new-password" maxLength="256"/></label>
   ${user.role==='admin'&&html`<label class="field"><span>Роль пользователя</span><select name="role" value=${role} onChange=${e=>setRole(e.currentTarget.value)}><option value="user">Пользователь</option><option value="admin">Администратор</option></select></label>`}
   <p class="warning">Все сессии и гостевые ссылки этого пользователя будут отозваны. Уже начатые передачи могут завершиться.${target.id===user.id?' После сохранения войдите заново.':''}</p>
   ${error&&html`<p class="form-error" role="alert">${error}</p>`}
   <div class="dialog-actions"><button type="button" class="button secondary" disabled=${busy} onClick=${onClose}>Отмена</button><button class="button" disabled=${busy}>Сохранить учётную запись</button></div>
  </form>
 </dialog>`;
}
