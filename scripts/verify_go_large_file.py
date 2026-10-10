"""Stream a real large file through HTTPS without materializing it in client RAM."""
from pathlib import Path
import argparse, hashlib, http.client, json, os, secrets, shutil, socket, ssl, subprocess, tempfile, time
import psutil
ROOT=Path(__file__).resolve().parents[1]
def main():
 parser=argparse.ArgumentParser();parser.add_argument('--gib',type=int,default=1);args=parser.parse_args()
 if not 1<=args.gib<=100:raise ValueError('Choose 1–100 GiB')
 total=args.gib*1024**3;chunk=bytes(1024**2)
 with tempfile.TemporaryDirectory(prefix='lanbridge-large-') as folder:
  root=Path(folder)
  if shutil.disk_usage(root).free<total+2*1024**3:raise RuntimeError('Insufficient temporary disk space')
  with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
  config=root/'config.yaml';config.write_text(f'server:\n  port: {port}\n',encoding='utf-8')
  env={k:v for k,v in os.environ.items() if not k.startswith('LANBRIDGE_GO_')}
  cookies={};csrf='';connection=None;process=None;peak=0;private_peak=0
  def connect():return http.client.HTTPSConnection('127.0.0.1',port,context=ssl._create_unverified_context(),timeout=300)
  def call(path,method='GET',body=None,headers=None):
   nonlocal csrf
   hdr={'Cookie':'; '.join(k+'='+v for k,v in cookies.items()),'X-CSRF-Token':csrf}
   if isinstance(body,dict):body=json.dumps(body).encode();hdr['Content-Type']='application/json'
   hdr.update(headers or {});connection.request(method,path,body,headers=hdr);response=connection.getresponse();raw=response.read()
   for key,value in response.getheaders():
    if key.lower()=='set-cookie':name,data=value.split(';',1)[0].split('=',1);cookies[name]=data
   if response.status>=400:raise RuntimeError(f'{method} {path}: {response.status}: {raw[:500]!r}')
   result=json.loads(raw)
   if 'csrf' in result:csrf=result['csrf']
   return result
  def start(log):
   nonlocal connection,process
   process=subprocess.Popen([str(ROOT/'dist/lanbridge.exe'),'-config',str(config)],stdout=log,stderr=log,env=env)
   for _ in range(100):
    connection=connect()
    try:call('/healthz');return
    except OSError:connection.close();time.sleep(.1)
   raise RuntimeError('Startup timeout')
  def measure():
   nonlocal peak,private_peak
   info=psutil.Process(process.pid).memory_info();peak=max(peak,info.rss);private_peak=max(private_peak,getattr(info,'private',info.vms))
  digest=hashlib.sha256()
  for _ in range(total//len(chunk)):digest.update(chunk)
  expected=digest.hexdigest()
  with (root/'server.log').open('w',encoding='utf-8') as log:
   try:
    start(log);call('/api/status');call('/api/setup','POST',{'username':'largeadmin','password':secrets.token_urlsafe(24)})
    upload=call('/api/uploads','POST',{'path':'large.bin','size':total,'sha256':expected,'conflict':'skip'})
    started=time.monotonic()
    for offset in range(0,total,len(chunk)):
     upload=call('/api/uploads/'+upload['id'],'PATCH',chunk,{'Upload-Offset':str(offset),'Content-Type':'application/octet-stream'})
     assert upload['offset']==offset+len(chunk)
     if offset==255*len(chunk):
      connection.close();process.terminate();process.wait(timeout=15);start(log)
      assert call('/api/uploads/'+upload['id'])['offset']==offset+len(chunk)
     if offset%(64*len(chunk))==0:measure()
     if (offset+len(chunk))%(1024**3)==0:print(f'Uploaded {(offset+len(chunk))//1024**3} GiB',flush=True)
    upload_seconds=time.monotonic()-started
    finish=time.monotonic();assert call('/api/uploads/'+upload['id']+'/finish','POST',{})['state']=='complete';finish_seconds=time.monotonic()-finish;measure()
    connection.request('GET','/api/files/download?path=large.bin',headers={'Cookie':'; '.join(k+'='+v for k,v in cookies.items())})
    response=connection.getresponse();assert response.status==200
    read=0;digest=hashlib.sha256();download_start=time.monotonic()
    while data:=response.read(len(chunk)):
     digest.update(data);read+=len(data)
     if read%(64*len(chunk))==0:measure()
    assert read==total and digest.hexdigest()==expected
    result={'result':'PASS','bytes_each_direction':total,'resumed_after_process_stop':True,'upload_seconds':round(upload_seconds,2),'server_hash_publish_seconds':round(finish_seconds,2),'download_seconds':round(time.monotonic()-download_start,2),'sampled_working_set_peak_bytes':peak,'sampled_private_peak_bytes':private_peak,'client_chunk_bytes':len(chunk)}
    out=ROOT/'.verification';out.mkdir(exist_ok=True);(out/'go-large-file.json').write_text(json.dumps(result,indent=2),encoding='utf-8');print(json.dumps(result,indent=2),flush=True)
   finally:
    if connection:connection.close()
    if process and process.poll() is None:process.terminate();process.wait(timeout=15)
   text=(root/'server.log').read_text(encoding='utf-8');assert '"level":"ERROR"' not in text,text[-2000:]
if __name__=='__main__':main()
