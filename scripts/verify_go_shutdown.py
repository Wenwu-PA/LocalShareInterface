"""Real OS signal while an HTTPS PATCH and a SQLite write transaction are active."""
from pathlib import Path
from contextlib import closing
import hashlib
import http.client
import json
import os
import secrets
import signal
import socket
import sqlite3
import ssl
import subprocess
import sys
import tempfile
import time

ROOT=Path(__file__).resolve().parents[1]


def stop_signal(process):
    if os.name != 'nt':
        process.send_signal(signal.SIGTERM)
        return
    # The server has its own hidden console. Signal only that console from a helper.
    code = '''import ctypes,sys,time
k=ctypes.WinDLL('kernel32',use_last_error=True)
k.FreeConsole()
if not k.AttachConsole(int(sys.argv[1])): raise ctypes.WinError(ctypes.get_last_error())
handler=ctypes.WINFUNCTYPE(ctypes.c_bool,ctypes.c_uint)(lambda event: True)
if not k.SetConsoleCtrlHandler(handler,True): raise ctypes.WinError(ctypes.get_last_error())
if not k.GenerateConsoleCtrlEvent(1,0): raise ctypes.WinError(ctypes.get_last_error())
time.sleep(.2)
k.FreeConsole()
'''
    subprocess.run([sys.executable,'-c',code,str(process.pid)],check=True)


def main():
    binary=Path(sys.argv[1]).resolve() if len(sys.argv)>1 else ROOT/'dist'/('lanbridge.exe' if os.name=='nt' else 'lanbridge-linux-amd64')
    with tempfile.TemporaryDirectory(prefix='lanbridge-shutdown-') as folder:
        root=Path(folder)
        with socket.socket() as probe:
            probe.bind(('127.0.0.1',0));port=probe.getsockname()[1]
        config=root/'config.yaml';config.write_text(f'server:\n  port: {port}\n',encoding='utf-8')
        cookies={};csrf='';process=None;active=None;db=None
        env={k:v for k,v in os.environ.items() if not k.startswith('LANBRIDGE_')}
        def connect():return http.client.HTTPSConnection('127.0.0.1',port,context=ssl._create_unverified_context(),timeout=15)
        def headers():return {'Cookie':'; '.join(k+'='+v for k,v in cookies.items()),'X-CSRF-Token':csrf}
        def call(path,method='GET',body=None,extra=None):
            nonlocal csrf
            connection=connect();hdr=headers()
            if isinstance(body,dict):body=json.dumps(body).encode();hdr['Content-Type']='application/json'
            hdr.update(extra or {})
            try:
                connection.request(method,path,body,hdr);response=connection.getresponse();raw=response.read()
                assert response.status<400,(path,response.status,raw[:250])
                for key,value in response.getheaders():
                    if key.lower()=='set-cookie':
                        name,value=value.split(';',1)[0].split('=',1);cookies[name]=value
                result=json.loads(raw)
                if isinstance(result,dict) and 'csrf' in result:csrf=result['csrf']
                return result
            finally:connection.close()
        with (root/'server.log').open('w',encoding='utf-8') as log:
            def start():
                options={}
                if os.name=='nt':
                    startup=subprocess.STARTUPINFO();startup.dwFlags|=subprocess.STARTF_USESHOWWINDOW;startup.wShowWindow=0
                    options={'creationflags':subprocess.CREATE_NEW_CONSOLE,'startupinfo':startup}
                proc=subprocess.Popen([str(binary),'-config',str(config)],stdout=log,stderr=log,env=env,**options)
                for _ in range(100):
                    try:call('/healthz');return proc
                    except OSError:
                        if proc.poll() is not None:raise RuntimeError('startup failed')
                        time.sleep(.1)
                proc.kill();proc.wait();raise RuntimeError('startup timeout')
            try:
                process=start();call('/api/status')
                call('/api/setup','POST',{'username':'shutdownadmin','password':secrets.token_urlsafe(24)})
                chunk=bytes(range(256))*4096;digest=hashlib.sha256(chunk*2).hexdigest()
                upload=call('/api/uploads','POST',{'path':'shutdown.bin','size':2*len(chunk),'sha256':digest,'conflict':'skip'})
                endpoint='/api/uploads/'+upload['id']
                call(endpoint,'PATCH',chunk,{'Upload-Offset':'0'})
                active=connect();active.putrequest('PATCH',endpoint)
                for k,v in headers().items():active.putheader(k,v)
                active.putheader('Upload-Offset',str(len(chunk)));active.putheader('Content-Length',str(len(chunk)))
                active.endheaders();active.send(chunk[:65536])
                # Observe actual bytes on disk before delivering the signal, not a sleep-only race.
                deadline=time.monotonic()+5
                while time.monotonic()<deadline:
                    partial=[p for p in root.rglob('*') if p.is_file() and p.stat().st_size==len(chunk)+65536]
                    if partial:break
                    time.sleep(.02)
                assert partial,'PATCH did not start writing'
                databases=list(root.rglob('lanbridge-go.sqlite3'));assert len(databases)==1
                db=sqlite3.connect(databases[0],timeout=5)
                db.execute('CREATE TABLE shutdown_probe(value TEXT NOT NULL)');db.commit()
                db.execute('BEGIN IMMEDIATE');db.execute("INSERT INTO shutdown_probe VALUES('committed during shutdown')")
                stop_signal(process)
                deadline=time.monotonic()+3
                while time.monotonic()<deadline:
                    try:
                        with socket.create_connection(('127.0.0.1',port),timeout=.1):pass
                    except OSError:break
                    time.sleep(.02)
                else:raise AssertionError('signal did not close listener')
                assert process.poll() is None,'server exited before active request drained'
                db.commit();db.close();db=None
                active.send(chunk[65536:]);response=active.getresponse();body=response.read()
                assert response.status==200,(response.status,body)
                assert json.loads(body)['offset']==2*len(chunk)
                active.close();active=None
                assert process.wait(timeout=15)==0,'nonzero shutdown exit'
                log.flush();logs=(root/'server.log').read_text(encoding='utf-8')
                assert 'server stopped cleanly' in logs and 'graceful shutdown timeout' not in logs,logs[-2000:]
                with closing(sqlite3.connect(databases[0])) as check:
                    assert check.execute('PRAGMA integrity_check').fetchone()[0]=='ok'
                    assert check.execute('SELECT value FROM shutdown_probe').fetchone()[0]=='committed during shutdown'
                assert not (root/'runtime/files/shutdown.bin').exists(), 'incomplete upload was published'
                process=start()
                assert call(endpoint)['offset']==2*len(chunk)
                assert call(endpoint+'/finish','POST',{})['state']=='complete'
                download=connect();download.request('GET','/api/files/download?path=shutdown.bin',headers=headers())
                response=download.getresponse();data=response.read();download.close()
                assert response.status==200 and hashlib.sha256(data).hexdigest()==digest
                stop_signal(process);assert process.wait(timeout=15)==0
                result={'result':'PASS','signal':'CTRL_BREAK_EVENT' if os.name=='nt' else 'SIGTERM',
                        'active_patch_drained':True,'concurrent_sqlite_write':True,'integrity_check':'ok',
                        'restart_resume_sha256':digest,'bytes':len(data),'graceful_exit':True}
                out=ROOT/'.verification/go-graceful-shutdown.json';out.parent.mkdir(exist_ok=True)
                out.write_text(json.dumps(result,indent=2),encoding='utf-8');print(json.dumps(result))
            finally:
                if db:db.rollback();db.close()
                if active:active.close()
                if process and process.poll() is None:process.kill();process.wait(timeout=10)


if __name__=='__main__':main()
