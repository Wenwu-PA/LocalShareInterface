import {sha256} from '@noble/hashes/sha2.js';
self.onmessage=async({data:file})=>{
 try{const hash=sha256.create();let at=0,last=0;while(at<file.size){const chunk=new Uint8Array(await file.slice(at,at+1048576).arrayBuffer());hash.update(chunk);at+=chunk.length;if(performance.now()-last>150){postMessage({progress:at});last=performance.now()}}postMessage({hash:Array.from(hash.digest(),byte=>byte.toString(16).padStart(2,'0')).join('')})}catch(e){postMessage({error:e.message})}
};
