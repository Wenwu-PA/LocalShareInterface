"""Frame timing of a rendered, scrolling viewport in the test browser."""


def measure_scroll_frames(page, selector, row_selector, seconds=8):
    return page.evaluate("""async ({selector,rowSelector,seconds})=>{
      const viewport=document.querySelector(selector);
      if(!viewport)throw new Error('Missing performance viewport: '+selector);
      if(document.hidden)throw new Error('Performance page is hidden');
      const original=viewport.scrollTop,frames=[],longTasks=[],shifts=[];
      const supported=PerformanceObserver.supportedEntryTypes;
      const observe=(type,collect)=>{
        if(!supported.includes(type))return null;
        const observer=new PerformanceObserver(list=>collect(list.getEntries()));
        observer.observe({type,buffered:false});return observer;
      };
      const tasks=observe('longtask',entries=>longTasks.push(...entries.map(e=>e.duration)));
      const layout=observe('layout-shift',entries=>shifts.push(...entries.filter(e=>!e.hadRecentInput).map(e=>e.value)));
      let last=null,started=null,maxRows=0,lastPosition=null,scrollChanges=0;
      try{
        await new Promise(resolve=>{
          function frame(now){
            if(started===null)started=now;
            if(last!==null)frames.push(now-last);
            last=now;
            const phase=((now-started)/2000)%2;
            viewport.scrollTop=Math.max(0,viewport.scrollHeight-viewport.clientHeight)*(phase<=1?phase:2-phase);
            if(lastPosition!==null&&viewport.scrollTop!==lastPosition)scrollChanges++;
            lastPosition=viewport.scrollTop;
            maxRows=Math.max(maxRows,viewport.querySelectorAll(rowSelector).length);
            if(now-started<seconds*1000)requestAnimationFrame(frame);else resolve();
          }
          requestAnimationFrame(frame);
        });
        if(document.hidden)throw new Error('Performance page became hidden');
        if(tasks)longTasks.push(...tasks.takeRecords().map(e=>e.duration));
        if(layout)shifts.push(...layout.takeRecords().filter(e=>!e.hadRecentInput).map(e=>e.value));
        const sorted=[...frames].sort((a,b)=>a-b),quantile=p=>sorted[Math.ceil(sorted.length*p)-1]||0;
        const duration=frames.reduce((a,b)=>a+b,0);
        if(!frames.length||!scrollChanges)throw new Error('No frame/scroll samples');
        return {seconds:duration/1000,frame_intervals:frames.length,average_raf_fps:frames.length*1000/duration,
          p50_frame_ms:quantile(.5),p95_frame_ms:quantile(.95),p99_frame_ms:quantile(.99),max_frame_ms:Math.max(...frames),
          frames_over_25ms:frames.filter(n=>n>25).length,frames_over_50ms:frames.filter(n=>n>50).length,
          long_tasks_supported:!!tasks,long_tasks:longTasks.length,long_task_total_ms:longTasks.reduce((a,b)=>a+b,0),
          layout_shift_supported:!!layout,layout_shift_without_recent_input:shifts.reduce((a,b)=>a+b,0),
          max_rendered_rows:maxRows,scroll_changes:scrollChanges,visibility:document.visibilityState};
      }finally{tasks?.disconnect();layout?.disconnect();viewport.scrollTop=original}
    }""", {'selector': selector, 'rowSelector': row_selector, 'seconds': seconds})
