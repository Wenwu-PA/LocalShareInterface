// Called only within the file panel. Keep browser/editor keys in editable fields.
export function fileShortcut(event, {editable=false, blocked=false, loading=false, selected=0}={}) {
 if(blocked||editable||event.defaultPrevented||event.isComposing||event.repeat||event.altKey)return null;
 const command=event.ctrlKey||event.metaKey;
 if(command&&!event.shiftKey){
  if(event.code==='KeyF')return 'search';
  if(event.code==='KeyA'&&!loading)return 'select';
 }
 if(command||event.shiftKey)return null;
 if(event.key==='Escape'&&selected)return 'clear';
 if(!loading&&event.key==='Delete'&&selected)return 'trash';
 if(!loading&&event.key==='F2'&&selected===1)return 'rename';
 return null;
}
