const $ = (s) => document.querySelector(s);
async function request(path, data) {
  const csrf = document.cookie.split('; ').find(x => x.startsWith('lanbridge_csrf='))?.split('=').slice(1).join('=');
  return fetch(path, {method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':decodeURIComponent(csrf || '')},credentials:'same-origin',body:JSON.stringify(data)});
}
(async()=>{
  try {
    const r=await fetch('/api/status',{credentials:'same-origin'}), s=await r.json();
    if(s.authenticated){location.replace('/app');return}
    if(s.setup_required){$('#auth-eyebrow').textContent='ПЕРВЫЙ ЗАПУСК · АДМИНИСТРАТОР';$('#auth-title').innerHTML='Создайте<br>своё пространство.';$('#auth-description').textContent='Придумайте логин и надёжный пароль, чтобы настроить LANBridge.';$('#auth-submit').innerHTML='Создать администратора <span>→</span>';}
    $('#auth-form').addEventListener('submit',async e=>{
      e.preventDefault(); const f=new FormData(e.currentTarget), body={username:f.get('username').trim(),password:f.get('password')};
      $('#auth-error').textContent=''; $('#auth-submit').disabled=true;
      try { const r=await request(s.setup_required?'/api/setup':'/api/login',body), result=await r.json(); if(!r.ok)throw Error(result.detail||'Не удалось выполнить запрос'); location.assign('/app'); }
      catch(err){$('#auth-error').textContent=err.message;$('#auth-submit').disabled=false;}
    });
  } catch(e){$('#auth-error').textContent='Не удалось связаться с сервером.';}
})();
