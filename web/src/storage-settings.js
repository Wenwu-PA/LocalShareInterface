import {h} from 'preact';
import {useEffect, useRef, useState} from 'preact/hooks';
import htm from 'htm';
const html = htm.bind(h);
const MiB = 1048576;
const fields = [
 ['global_limit', 'Общая квота, МиБ', 0, null, MiB],
 ['user_limit', 'Квота пользователя, МиБ', 0, null, MiB],
 ['critical_free', 'Резерв свободного места, МиБ', 16, null, MiB],
 ['trash_limit', 'Лимит корзины, МиБ', 0, null, MiB],
 ['trash_days', 'Хранение корзины, дней', 1, 365, 1],
 ['partial_hours', 'Хранение докачки, часов', 1, 720, 1],
];
export function StorageSettings({settings, api, onClose, onSaved}) {
 const ref = useRef();
 const [busy, setBusy] = useState(false), [error, setError] = useState('');
 useEffect(() => {ref.current.showModal()}, []);
 async function save(event) {
  event.preventDefault();
  if (busy) return;
  const form = new FormData(event.currentTarget), values = {};
  for (const [key, label, min, max, scale] of fields) {
   const value = Number(form.get(key)), bytes = value * scale;
   if (!Number.isFinite(value) || value < min || (max !== null && value > max) || !Number.isSafeInteger(bytes)) {
    setError('Проверьте поле «' + label + '»: требуется допустимое точное значение.');
    return;
   }
   values[key] = bytes;
  }
  setBusy(true);setError('');
  try {onSaved(await api('files/storage', 'PUT', values))}
  catch (e) {setError(e.message)}
  finally {setBusy(false)}
 }
 return html`<dialog ref=${ref} class="monitor-dialog" onCancel=${event=>{if(busy)event.preventDefault();else onClose()}}>
  <div class="panel-heading"><h2>Настройки хранилища</h2><button class="icon-button" disabled=${busy} onClick=${onClose} aria-label="Закрыть">×</button></div>
  <p class="muted">0 в квоте или лимите корзины — без ограничения. 1 МиБ = 1 048 576 байт.</p>
  <form onSubmit=${save}>
   <fieldset disabled=${busy} class="storage-fields"><div class="field-pair">
    ${fields.map(([key,label,min,max,scale])=>html`<label class="field" key=${key}><span>${label}</span><input name=${key} type="number" required min=${min} max=${max??Number.MAX_SAFE_INTEGER/scale} step=${scale===1?1:'any'} defaultValue=${settings[key]/scale}/></label>`)}
   </div></fieldset>
   <p class="warning">После уменьшения срока или лимита корзины старые объекты могут быть удалены при ближайшей очистке. Сокращение срока докачки может удалить незавершённые передачи.</p>
   ${error&&html`<p class="form-error" role="alert">${error}</p>`}
   <div class="dialog-actions"><button type="button" class="button secondary" disabled=${busy} onClick=${onClose}>Отмена</button><button class="button" disabled=${busy}>${busy?'Сохранение…':'Сохранить настройки'}</button></div>
  </form>
 </dialog>`;
}
