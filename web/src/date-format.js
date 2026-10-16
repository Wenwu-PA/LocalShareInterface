const formatters=new Map();
export function formatDate(milliseconds, {timezone, date_format='dmy'}={}, mode='datetime') {
 const date=new Date(milliseconds);
 if(milliseconds===null||milliseconds===undefined||!Number.isFinite(date.getTime()))return '—';
 const key=timezone||'';
 let formatter=formatters.get(key);
 if(!formatter){
  const options={year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit',hourCycle:'h23'};
  try{formatter=new Intl.DateTimeFormat('en-GB',{...options,...(timezone?{timeZone:timezone}:{})})}
  catch{formatter=new Intl.DateTimeFormat('en-GB',{...options,timeZone:'UTC'})}
  if(formatters.size>=16)formatters.clear();
  formatters.set(key,formatter);
 }
 const p=Object.fromEntries(formatter.formatToParts(date).map(part=>[part.type,part.value]));
 const day=date_format==='iso'?`${p.year}-${p.month}-${p.day}`:`${p.day}.${p.month}.${p.year}`;
 const time=`${p.hour}:${p.minute}:${p.second}`;
 return mode==='date'?day:mode==='time'?time:day+', '+time;
}
