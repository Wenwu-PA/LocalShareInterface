export function shareURL(origin, suffix) {
 const url=new URL(origin);
 if(url.protocol!=='https:'||url.username||url.password||url.pathname!=='/'||url.search||url.hash||origin.length>512)throw new Error('Укажите HTTPS-адрес сервера без пути, пароля и параметров');
 return url.origin+suffix;
}
export function localOnly(origin){const host=new URL(origin).hostname;return host==='localhost'||host==='[::1]'||host.startsWith('127.')}
