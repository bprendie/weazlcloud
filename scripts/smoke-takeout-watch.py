#!/usr/bin/env python3
"""Real Docker/API smoke of unattended import, corrupt-entry logging and cleanup."""
import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.request
import uuid
import zipfile
from pathlib import Path
sys.path.insert(0,str(Path(__file__).resolve().parent/'takeout_watch'))
from watch import Watch
from support import atomic_json

name='weazl-watch-smoke-'+uuid.uuid4().hex[:8]
image=os.environ.get('WEAZLCLOUD_IMAGE','weazlcloud:takeout-watch-smoke')
try:
    with tempfile.TemporaryDirectory(prefix='weazl-watch-smoke-') as tmp:
        root=Path(tmp);root.chmod(0o755)
        stage=root/'stage';stage.mkdir(mode=0o755)
        data=root/'data';data.mkdir()
        work=root/'work';work.mkdir()
        for filename,entries in {
            'takeout-smoke-1-001.zip':{'Takeout/Drive/good.txt':b'keep this','Takeout/Drive/bad.txt':b'unique-payload'},
            'takeout-smoke-1-002.zip':{'Takeout/Google Photos/Album/photo.jpg':b'photo','Takeout/Google Photos/Album/metadata.json':b'{"title":"Smoke album"}'},
            'takeout-smoke-2-001.zip':{'Takeout/Drive/last.txt':b'last file'},
        }.items():
            with zipfile.ZipFile(stage/filename,'w',compression=zipfile.ZIP_STORED) as z:
                for path,body in entries.items():z.writestr(path,body)
        damaged=stage/'takeout-smoke-1-001.zip'
        damaged.write_bytes(damaged.read_bytes().replace(b'unique-payload',b'BROKEN-payload',1))
        (work/'creds.md').write_text('Username: `bobp`\nPassword: `watch-test-pass`\n')
        (work/'creds.md').chmod(0o600)
        subprocess.run(['docker','run','-d','--name',name,'--user',f'{os.getuid()}:{os.getgid()}','-p','127.0.0.1:7272:7272','-e','WEAZLCLOUD_DATA=/data','-e','WEAZLCLOUD_DESK_ADDR=:7272','-e','WEAZLCLOUD_IMPORT_DIR=/import','-e','WEAZLCLOUD_IMPORT_OWNER=bobp','-v',str(data)+':/data','-v',str(stage)+':/import:ro',image],check=True,capture_output=True)
        for _ in range(80):
            try:
                with urllib.request.urlopen('http://127.0.0.1:7272/ready',timeout=2):break
            except Exception:time.sleep(.25)
        else:raise AssertionError('container not ready')
        request=urllib.request.Request('http://127.0.0.1:7272/api/bootstrap',data=json.dumps({'username':'bobp','password':'watch-test-pass','vault_passphrase':'watch-test-pass','confirm':'watch-test-pass'}).encode(),headers={'Content-Type':'application/json','X-Weazl-Desk':'1'})
        with urllib.request.urlopen(request):pass
        cfg={'work':str(work),'stage':str(stage),'data':str(data),'prefix':'takeout-smoke-','last_name':'takeout-smoke-2-001.zip','minimum_files':3,'api':'http://127.0.0.1:7272','owner':'bobp','credentials':str(work/'creds.md')}
        watcher=Watch(cfg)
        watcher.run()
        assert watcher.state['phase']=='waiting'
        watcher.state['stable_since']-=1200;watcher.save()
        for _ in range(80):
            watcher=Watch(cfg);watcher.run()
            if watcher.state['phase']=='complete':break
            time.sleep(.5)
        else:raise AssertionError('batch did not finish')
        report=json.loads((work/'report.json').read_text())
        errors=json.loads((work/'corrupt-files.json').read_text())
        assert report['landed_files']==4,report
        assert report['skipped_corrupt_files']==1,report
        assert report['physical_repository_bytes']>0,report
        assert errors['files'][0]['path']=='Takeout/Drive/bad.txt',errors
        assert not list(stage.iterdir()),'staging not cleared'
        assert all(x['status']=='removed' for x in report['archives'].values())
        print('PASS: wait gate, skip corrupt entry, Drive/Photos import, stored hash verification, resumable watcher, cleanup and disk report')
finally:
    subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
