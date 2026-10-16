import {topLevelSelection} from './file-selection.js';

export function bulkPlan(paths, action, destination='.', conflict='skip') {
 if(!['copy','move','trash'].includes(action))throw new Error('Неизвестная операция');
 if(paths.length>1000)throw new Error('Выберите не более 1000 объектов');
 const folder=destination.trim().replaceAll('\\','/').replace(/\/+$/,'');
 if(action!=='trash'&&(!folder||folder.startsWith('/')||folder.split('/').some(part=>!part||part==='..'||part==='.'&&folder!=='.')||folder.includes(':')))
  throw new Error('Укажите папку относительно общей папки или точку для корня');
 return topLevelSelection(paths).map(source=>({action,source,...(action==='trash'?{}:{target:folder==='.'?source.split('/').at(-1):folder+'/'+source.split('/').at(-1),conflict})}));
}

export async function runBulk(plan, api, stopped, progress) {
 const failed=[];let success=0,processed=0;
 for(const operation of plan){
  if(stopped())break;
  let fatal=false;
  try{await api('files/operation','POST',operation);success++}
  catch(e){failed.push({path:operation.source,message:e.message});fatal=!e.status||e.status>=500||e.status===401}
  progress(++processed);
  if(fatal)break;
 }
 return {success,failed,pending:plan.length-processed};
}
