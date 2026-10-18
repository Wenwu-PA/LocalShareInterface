"""Bounded local monitor + file-transfer soak; all server data is disposable."""
from pathlib import Path
from collections import deque
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import argparse
import hashlib
import http.client
import json
import os
import secrets
import shutil
import socket
import ssl
import subprocess
import tempfile
import threading
import time
import uuid
import psutil

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', default=str(ROOT/'dist/lanbridge.exe'))
    parser.add_argument('--seconds', type=int, default=120)
    parser.add_argument('--monitors', type=int, default=50)
    parser.add_argument('--file-mib', type=int, default=8)
    parser.add_argument('--pause', type=float, default=2)
    parser.add_argument('--max-rss-mib', type=int, default=0)
    args = parser.parse_args()
    if not 10 <= args.seconds <= 7*86400 or not 1 <= args.monitors <= 1000 or not 1 <= args.file_mib <= 256 or not 0 <= args.pause <= 60 or not 0 <= args.max_rss_mib <= 65536:
        parser.error('seconds 10..604800, monitors 1..1000, file-mib 1..256, pause 0..60, max-rss-mib 0..65536 (0 disables cap)')
    binary = Path(args.binary).resolve()
    if not binary.is_file():
        parser.error('Build the selected binary first')
    output = ROOT/'.verification'/('soak-'+uuid.uuid4().hex[:12])
    output.mkdir(parents=True)
    result = {'result':'RUNNING','requested_seconds':args.seconds,'monitors':args.monitors,
              'file_bytes':args.file_mib*1024**2,'max_rss_mib':args.max_rss_mib,'report':str(output),'full_day':False}
    digest=hashlib.sha256()
    with binary.open('rb') as binary_file:
        for block in iter(lambda:binary_file.read(1024**2),b''):digest.update(block)
    result['binary_sha256']=digest.hexdigest()
    result['revision']=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()
    (output/'result.json').write_text(json.dumps(result,indent=2),encoding='utf-8')
    print(json.dumps(result),flush=True)

    class Target(BaseHTTPRequestHandler):
        def log_message(self, *_): pass
        def do_GET(self):
            self.send_response(204)
            self.end_headers()
    target = ThreadingHTTPServer(('127.0.0.1',0),Target)
    threading.Thread(target=target.serve_forever,daemon=True).start()
    process = connection = None
    try:
        with tempfile.TemporaryDirectory(prefix='lanbridge-soak-') as folder:
            root = Path(folder)
            if shutil.disk_usage(root).free < 2*args.file_mib*1024**2+1024**3:
                raise RuntimeError('Need room for two file copies plus 1 GiB')
            with socket.socket() as probe:
                probe.bind(('127.0.0.1',0));port=probe.getsockname()[1]
            config = root/'config.yaml'
            config.write_text(f'server:\n  host: 127.0.0.1\n  port: {port}\n',encoding='utf-8')
            env = {k:v for k,v in os.environ.items() if not k.startswith('LANBRIDGE_')}
            cookies, csrf = {}, ''
            def headers():
                return {'Cookie':'; '.join(k+'='+v for k,v in cookies.items()),'X-CSRF-Token':csrf}
            def call(path, method='GET', body=None, extra=None):
                nonlocal csrf
                hdr = headers()
                if isinstance(body,dict):
                    body=json.dumps(body).encode();hdr['Content-Type']='application/json'
                hdr.update(extra or {})
                connection.request(method,path,body,headers=hdr)
                response=connection.getresponse();raw=response.read()
                for key,value in response.getheaders():
                    if key.lower()=='set-cookie':
                        name,data=value.split(';',1)[0].split('=',1);cookies[name]=data
                if response.status>=400:
                    raise RuntimeError(f'{method} {path}: HTTP {response.status}: {raw[:300]!r}')
                value=json.loads(raw)
                if isinstance(value,dict) and 'csrf' in value:csrf=value['csrf']
                return value
            with (root/'server.log').open('w',encoding='utf-8') as log, (output/'samples.jsonl').open('w',encoding='utf-8') as samples:
                process=subprocess.Popen([str(binary),'-config',str(config)],stdout=log,stderr=log,env=env,
                                         creationflags=subprocess.CREATE_NO_WINDOW if os.name=='nt' else 0)
                result['server_pid']=process.pid
                try:
                    for _ in range(100):
                        connection=http.client.HTTPSConnection('127.0.0.1',port,context=ssl._create_unverified_context(),timeout=20)
                        try:call('/healthz');break
                        except OSError:
                            connection.close()
                            if process.poll() is not None:raise RuntimeError('Server exited during startup')
                            time.sleep(.1)
                    else:raise RuntimeError('Server startup timeout')
                    call('/api/status')
                    credentials={'username':'soakadmin','password':secrets.token_urlsafe(24)}
                    call('/api/setup','POST',credentials)
                    for i in range(args.monitors):
                        call('/api/monitors','POST',{'name':f'Soak {i}','kind':'http','target':f'http://127.0.0.1:{target.server_port}',
                            'group':'LAN','interval':20,'timeout':5,'retries':2,'expected_status':204,'method':'GET'})
                    observed=psutil.Process(process.pid);initial_cpu=observed.cpu_times()
                    started=time.monotonic();last_sample=last_login=started;cycles=0;peak_rss=peak_private=0;first_rss=None
                    def measure(force=False):
                        nonlocal peak_rss,peak_private,last_sample,first_rss
                        memory=observed.memory_info()
                        peak_rss=max(peak_rss,memory.rss);peak_private=max(peak_private,getattr(memory,'private',memory.vms))
                        if first_rss is None:first_rss=memory.rss
                        if args.max_rss_mib and memory.rss>args.max_rss_mib*1024**2:
                            raise RuntimeError(f'Working Set {memory.rss} exceeds {args.max_rss_mib} MiB budget')
                        now=time.monotonic()
                        if force or now-last_sample>=10:
                            resources=call('/api/resources')
                            sample={'seconds':round(now-started,2),'cycles':cycles,'rss':memory.rss,
                                    'private':getattr(memory,'private',memory.vms),
                                    **{key:resources[key] for key in ('heap_bytes','goroutines','database_bytes','wal_bytes')}}
                            samples.write(json.dumps(sample)+'\n');samples.flush();last_sample=now
                            print(json.dumps(sample),flush=True)
                    measure(True)
                    while time.monotonic()-started<args.seconds:
                        if time.monotonic()-last_login>=3600:
                            call('/api/login','POST',credentials);last_login=time.monotonic()
                        chunk=bytes([cycles%251])*(1024**2)
                        expected=hashlib.sha256()
                        for _ in range(args.file_mib):expected.update(chunk)
                        upload=call('/api/uploads','POST',{'path':'soak.bin','size':args.file_mib*len(chunk),'sha256':expected.hexdigest(),'conflict':'replace'})
                        for offset in range(0,args.file_mib*len(chunk),len(chunk)):
                            state=call('/api/uploads/'+upload['id'],'PATCH',chunk,{'Upload-Offset':str(offset),'Content-Type':'application/octet-stream'})
                            assert state['offset']==offset+len(chunk)
                            measure()
                        assert call('/api/uploads/'+upload['id']+'/finish','POST',{})['state']=='complete'
                        connection.request('GET','/api/files/download?path=soak.bin',headers=headers())
                        response=connection.getresponse()
                        assert response.status==200
                        received=0;actual=hashlib.sha256()
                        while data:=response.read(1024**2):
                            received+=len(data);actual.update(data)
                        assert received==args.file_mib*len(chunk) and actual.hexdigest()==expected.hexdigest()
                        cycles+=1;measure()
                        remaining=args.seconds-(time.monotonic()-started)
                        if remaining>0:time.sleep(min(args.pause,remaining))
                    measure(True)
                    monitors=call('/api/monitors')
                    assert len(monitors)==args.monitors and all(m['state']['status']=='up' for m in monitors), 'monitor failed under transfer load'
                    elapsed=time.monotonic()-started;cpu=observed.cpu_times()
                    result.update(result='PASS',seconds=round(elapsed,2),full_day=elapsed>=86400,cycles=cycles,
                                  bytes_each_direction=cycles*args.file_mib*1024**2,working_set_peak=peak_rss,
                                  private_peak=peak_private,rss_start=first_rss,rss_end=observed.memory_info().rss,
                                  cpu_percent_one_core=round(100*(cpu.user+cpu.system-initial_cpu.user-initial_cpu.system)/elapsed,3))
                finally:
                    if connection:connection.close()
                    if process and process.poll() is None:process.terminate();process.wait(timeout=15)
                    log.flush()
                    with (root/'server.log').open(encoding='utf-8') as diagnostic:
                        tail=deque(maxlen=100);errors=0
                        for line in diagnostic:
                            tail.append(line)
                            if '"level":"ERROR"' in line or 'panic:' in line.lower():errors+=1
                        (output/'server-tail.log').write_text(''.join(tail),encoding='utf-8')
                        result['server_errors']=errors
            assert result['server_errors']==0, 'Server logged errors; inspect server-tail.log'
    except BaseException as error:
        result.update(result='FAIL',error=str(error))
        raise
    finally:
        target.shutdown();target.server_close()
        (output/'result.json').write_text(json.dumps(result,indent=2),encoding='utf-8')
        print(json.dumps(result),flush=True)


if __name__=='__main__':main()
