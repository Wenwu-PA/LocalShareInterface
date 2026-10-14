// Selection uses loaded paths, so it also covers virtualized rows outside the DOM.
export function selectFileRange(previous, paths, anchor, target, checked, shift, limit=1000) {
 const end=paths.indexOf(target), start=shift?paths.indexOf(anchor):-1;
 if(end<0)return {selected:previous,limited:false};
 const range=start<0?[target]:paths.slice(Math.min(start,end),Math.max(start,end)+1);
 const selected=new Set(previous);let limited=false;
 for(const path of range){
  if(!checked)selected.delete(path);
  else if(!selected.has(path)){if(selected.size>=limit)limited=true;else selected.add(path)}
 }
 return {selected:[...selected],limited};
}
