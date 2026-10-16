export const internalDragType='application/x-lanbridge-selection';
export function dragOver(event){
 if(!Array.from(event.dataTransfer.types).some(type=>type==='Files'||type===internalDragType))return;
 event.preventDefault();event.stopPropagation();
 event.dataTransfer.dropEffect=event.dataTransfer.types.includes(internalDragType)?'move':'copy';
 event.currentTarget.classList.add('file-drop-target');
}
export function dragLeave(event){event.currentTarget.classList.remove('file-drop-target')}
