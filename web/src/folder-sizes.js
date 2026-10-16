import {useEffect,useState} from 'preact/hooks';

export function useFolderSizes(api,paths,revision) {
 const[data,setData]=useState({});
 const key=JSON.stringify(paths);
 useEffect(()=>{
  let active=true,busy=false,timer;
  setData({});
  async function refresh(){
   if(document.hidden||busy||!paths.length)return;
   clearTimeout(timer);busy=true;
   let pending=false;
   try{
    const next=await api('files/folder-sizes','POST',{paths});
    if(active){setData(next);pending=Object.values(next).some(v=>v.state==='pending'||v.state==='busy')}
   }catch{if(active)setData({})}
   finally{busy=false;if(active)timer=setTimeout(refresh,pending?750:30000)}
  }
  refresh();document.addEventListener('visibilitychange',refresh);
  return()=>{active=false;clearTimeout(timer);document.removeEventListener('visibilitychange',refresh)};
 },[api,key,revision]);
 return data;
}
