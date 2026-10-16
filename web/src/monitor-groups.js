export const defaultGroupOrder=['WAN','LAN','DNS','Хранилище','Heartbeat'];
export const groupKey=name=>(name?.trim()||'Без группы').toLowerCase();
export function monitorGroups(monitors, order=defaultGroupOrder) {
 const groups=new Map();
 for(const name of order||defaultGroupOrder)groups.set(groupKey(name),{key:groupKey(name),name,members:[]});
 const extra=new Map();
 for(const monitor of monitors){
  const name=monitor.group?.trim()||'Без группы',key=groupKey(name);
  if(!groups.has(key)&&!extra.has(key))extra.set(key,{key,name,members:[]});
  (groups.get(key)||extra.get(key)).members.push(monitor);
 }
 return [...groups.values(),...Array.from(extra.values()).sort((a,b)=>a.name.localeCompare(b.name,'ru'))].map(group=>{
  const active=group.members.filter(m=>!m.paused&&m.state.status!=='paused');
  const status=!group.members.length?'empty':!active.length?'paused':active.some(m=>m.state.status==='down')?'down':active.every(m=>m.state.status==='up')?'up':'pending';
  const responding=active.filter(m=>m.state.status==='up'&&m.state.checked&&Number.isFinite(m.state.latency));
  return {...group,status,latency:responding.length?responding.reduce((sum,m)=>sum+m.state.latency,0)/responding.length:null};
 });
}
