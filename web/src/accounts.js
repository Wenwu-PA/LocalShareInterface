import {t} from './i18n/index.js';
import {h} from 'preact';
import {useEffect,useId,useRef,useState} from 'preact/hooks';
import htm from 'htm';
const html=htm.bind(h);
export function AccountDialog({target,user,api,onClose,onSaved}){
 const passwordHintId=useId();
 const ref=useRef(),[busy,setBusy]=useState(false),[error,setError]=useState(''),[role,setRole]=useState(target.role);
 useEffect(()=>{ref.current.showModal()},[]);
 async function submit(event){
  event.preventDefault();if(busy)return;
  const form=new FormData(event.currentTarget),body={current_password:form.get('current_password')};
  const password=form.get('password');
  if(password){if(password!==form.get('confirm')){setError(t('account.passwordMismatch'));return}body.password=password}
  if(user.role==='admin'&&form.get('role')!==target.role)body.role=form.get('role');
  if(!body.password&&!body.role){setError(t('account.noChanges'));return}
  setBusy(true);setError('');
  try{onSaved(await api('users/'+target.id,'PATCH',body))}catch(e){setError(e.message)}finally{setBusy(false)}
 }
 return html`<dialog ref=${ref} onCancel=${e=>{if(busy)e.preventDefault();else onClose()}}>
  <header><h2>${t('account.title',{username:target.username})}</h2><button class="icon-button" disabled=${busy} onClick=${onClose} aria-label=${t('account.close')}>×</button></header>
  <form onSubmit=${submit}>
   <label class="field"><span>${t('account.currentPassword')}</span><input name="current_password" type="password" autoComplete="current-password" required maxLength="256"/></label>
   <label class="field"><span>${t('account.newPassword')}</span><input name="password" type="password" aria-label=${t('account.newPassword')} aria-describedby=${passwordHintId} autoComplete="new-password" minLength="12" maxLength="256"/><small id=${passwordHintId}>${t('account.roleOnlyHint')}</small></label>
   <label class="field"><span>${t('account.confirmPassword')}</span><input name="confirm" type="password" autoComplete="new-password" maxLength="256"/></label>
   ${user.role==='admin'&&html`<label class="field"><span>${t('account.role')}</span><select name="role" value=${role} onChange=${e=>setRole(e.currentTarget.value)}><option value="user">${t('account.user')}</option><option value="admin">${t('account.admin')}</option></select></label>`}
   <p class="warning">${t('account.revokeWarning')}${target.id===user.id?' '+t('account.loginAgain'):''}</p>
   ${error&&html`<p class="form-error" role="alert">${error}</p>`}
   <div class="dialog-actions"><button type="button" class="button secondary" disabled=${busy} onClick=${onClose}>${t('account.cancel')}</button><button class="button" disabled=${busy}>${t('account.save')}</button></div>
  </form>
 </dialog>`;
}
