"""Paired, simultaneous 10-minute empty/history database comparison."""
from pathlib import Path
import json,os,subprocess,sys,time,uuid
ROOT=Path(__file__).resolve().parents[1]
out=ROOT/'.verification'/('cpu-history-'+uuid.uuid4().hex[:12]);out.mkdir(parents=True)
start=time.time()+60
processes=[]
try:
 for hours in [0,48]:
  log=(out/f'{hours}h.log').open('w',encoding='utf-8')
  env=dict(os.environ,PYTHONIOENCODING='utf-8')
  p=subprocess.Popen([sys.executable,'-u',str(ROOT/'scripts/verify_go_soak.py'),'--seconds','600','--monitors','50','--file-mib','8','--pause','5','--sample-seconds','60','--history-hours',str(hours),'--benchmark-start-at',str(start)],cwd=ROOT,stdout=log,stderr=log,env=env)
  processes.append((hours,p,log))
 print(json.dumps({'report':str(out),'start_epoch':start,'mode':'paired simultaneous separate processes'}),flush=True)
 results={}
 for hours,p,log in processes:
  code=p.wait();log.close()
  entries=[]
  for line in (out/f'{hours}h.log').read_text(encoding='utf-8').splitlines():
   try:entries.append(json.loads(line))
   except ValueError:pass
  final=next((x for x in reversed(entries) if 'cpu_ms_per_transfer_cycle' in x),None)
  if code or not final or final['result']!='PASS':raise RuntimeError(f'{hours}h failed; see {out}')
  results[str(hours)]=final
 (out/'comparison.json').write_text(json.dumps(results,indent=2),encoding='utf-8')
 print(json.dumps(results),flush=True)
finally:
 for _,p,log in processes:
  if p.poll() is None:p.terminate();p.wait(timeout=30)
  log.close()
