import ru from './ru.js';
export const defaultLocale='ru';
export const supportedLocales=Object.freeze(['ru']);
// Return text only; callers render it as text nodes, never as HTML.
export function createTranslator(catalogs,fallback=defaultLocale){
 const own=(object,key)=>object!=null&&Object.hasOwn(object,key);
 const lookup=(locale,key)=>own(catalogs,locale)&&own(catalogs[locale],key)?catalogs[locale][key]:undefined;
 return (key,values={},locale=fallback)=>{
  const message=lookup(locale,key)??lookup(fallback,key);
  if(typeof message!=='string')return key;
  return message.replace(/\{([a-zA-Z0-9_]+)\}/g,(match,name)=>own(values,name)?String(values[name]):match);
 };
}
export const t=createTranslator({ru});
