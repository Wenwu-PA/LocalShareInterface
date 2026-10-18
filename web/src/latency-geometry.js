// Preserve both extrema per horizontal bucket, plus the two endpoints.
// Coordinates retain their position in the original sequence after sampling.
export function latencyGeometry(input,width=640){
 const points=(Array.isArray(input)?input:[]).filter(p=>p&&Number.isFinite(p.at)&&Number.isFinite(p.latency)&&p.latency>=0);
 if(!points.length)return {line:'',max:10,first:null,last:null,count:0};
 const buckets=Math.max(1,Math.min(640,Math.floor(Number.isFinite(width)?width:640)));
 let max=10;for(const p of points)max=Math.max(max,p.latency);
 let indices;
 if(points.length<=2*buckets+2)indices=points.map((_,i)=>i);
 else{
  indices=[0];const middle=points.length-2;
  for(let bucket=0;bucket<buckets;bucket++){
   const start=1+Math.floor(bucket*middle/buckets),end=1+Math.floor((bucket+1)*middle/buckets);
   let low=start,high=start;
   for(let i=start+1;i<end;i++){
    if(points[i].latency<points[low].latency)low=i;
    if(points[i].latency>points[high].latency)high=i;
   }
   indices.push(...(low===high?[low]:low<high?[low,high]:[high,low]));
  }
  indices.push(points.length-1);
 }
 return {max,first:points[0],last:points.at(-1),count:indices.length,
  line:indices.map(i=>`${i/Math.max(1,points.length-1)*640},${170-points[i].latency/max*150}`).join(' ')};
}
