// Historical timestamps have second precision; these are approximate elapsed rates.
export function transferStats(item) {
 if (!Number.isSafeInteger(item.started) || !Number.isSafeInteger(item.finished) || item.started<=0 || item.finished<item.started) return {duration:'—',speed:null};
 const seconds=item.finished-item.started;
 if (!Number.isSafeInteger(seconds)) return {duration:'—',speed:null};
 const duration=seconds===0?'менее 1 с':seconds<60?seconds+' с':seconds<3600?Math.floor(seconds/60)+' мин '+seconds%60+' с':Math.floor(seconds/3600)+' ч '+Math.floor(seconds%3600/60)+' мин';
 const speed=seconds>0 && Number.isSafeInteger(item.size) && item.size>=0 ? item.size/seconds : null;
 return {duration,speed};
}
