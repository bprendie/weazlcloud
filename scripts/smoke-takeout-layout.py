#!/usr/bin/env python3
"""Exercise legacy import -> offline migration -> root-layout resume in Docker."""
import hashlib
import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request
import uuid
import zipfile
from pathlib import Path
sys.path.insert(0,str(Path(__file__).resolve().parent/'takeout_watch'))
from support import API

old_image=os.environ.get('WEAZLCLOUD_OLD_IMAGE','weazlcloud:takeout-batch-smoke')
new_image=os.environ.get('WEAZLCLOUD_IMAGE','weazlcloud:layout-smoke')
name='weazl-layout-smoke-'+uuid.uuid4().hex[:8]

def start(image,root):
    subprocess.run(['docker','run','-d','--name',name,'--user',f'{os.getuid()}:{os.getgid()}','-p','127.0.0.1:7272:7272','-e','WEAZLCLOUD_DATA=/data','-e','WEAZLCLOUD_DESK_ADDR=:7272','-e','WEAZLCLOUD_IMPORT_DIR=/import','-e','WEAZLCLOUD_IMPORT_OWNER=bobp','-v',str(root/'data')+':/data','-v',str(root/'stage')+':/import:ro',image],check=True,capture_output=True)
    for _ in range(120):
        try:
            with urllib.request.urlopen('http://127.0.0.1:7272/ready',timeout=2):return
        except Exception:time.sleep(.25)
    raise AssertionError('container did not become ready')

def job(api,filename):
    api.json('/api/takeout',{'name':filename})
    for _ in range(120):
        result=next(j for j in api.json('/api/takeout')['jobs'] if j['name']==filename)
        if result['status']!='running':return result
        time.sleep(.25)
    raise AssertionError('import timeout')

try:
    with tempfile.TemporaryDirectory(prefix='weazl-layout-') as tmp:
        root=Path(tmp);root.chmod(0o755)
        (root/'data').mkdir();(root/'stage').mkdir(mode=0o755)
        creds=root/'creds.md';creds.write_text('Username: `bobp`\nPassword: `layout-test-pass`\n');creds.chmod(0o600)
        entries={'Takeout/Drive/Trip /one.txt':b'keep drive bytes','Takeout/Google Photos/Trip/photo.jpg':b'keep photo bytes','Takeout/Google Photos/Trip/metadata.json':b'{"title":"Original album"}'}
        for filename,members in [('first.zip',entries),('second.zip',{'Takeout/Drive/Trip /two.txt':b'next part'})]:
            with zipfile.ZipFile(root/'stage'/filename,'w') as z:
                for p,b in members.items():z.writestr(p,b)
        cfg={'api':'http://127.0.0.1:7272','owner':'bobp','credentials':str(creds)}
        start(old_image,root)
        request=urllib.request.Request(cfg['api']+'/api/bootstrap',data=json.dumps({'username':'bobp','password':'layout-test-pass','vault_passphrase':'layout-test-pass','confirm':'layout-test-pass'}).encode(),headers={'Content-Type':'application/json','X-Weazl-Desk':'1'})
        with urllib.request.urlopen(request):pass
        api=API(cfg)
        owner=api.owner_id
        try:
            assert job(api,'first.zip')['status']=='complete'
            failed=job(api,'second.zip')
            assert failed['status']=='failed' and 'conflict' in failed['error'],failed
        finally:api.close()
        subprocess.run(['docker','stop',name],check=True,capture_output=True)
        subprocess.run(['docker','rm',name],check=True,capture_output=True)
        catalog=root/'data'/'users'/owner/'catalog.enc'
        before=catalog.read_bytes()
        command=['docker','run','--rm','-i','--network','none','--user',f'{os.getuid()}:{os.getgid()}','-v',str(root/'data')+':/data','--entrypoint','/usr/local/bin/takeout-layout',new_image,'--user-dir','/data/users/'+owner]
        secret=json.dumps({'passphrase':'layout-test-pass'})
        dry=json.loads(subprocess.check_output(command,input=secret,text=True))
        assert dry['files']==3 and not dry['applied'],dry
        assert catalog.read_bytes()==before,'dry run wrote catalog'
        applied=json.loads(subprocess.check_output(command+['--apply'],input=secret,text=True))
        assert applied['applied'] and applied['files']==3,applied
        assert catalog.with_name('catalog.enc.pre-root-v2').read_bytes()==before
        again=json.loads(subprocess.check_output(command+['--apply'],input=secret,text=True))
        assert again['changed_paths']==0,again
        start(new_image,root)
        api=API(cfg)
        try:
            for path,expected in [('Trip/one.txt',entries['Takeout/Drive/Trip /one.txt']),('Photos/Trip/photo.jpg',entries['Takeout/Google Photos/Trip/photo.jpg'])]:
                with api.open('/api/library?path='+urllib.parse.quote(path,safe='')) as response:assert response.read()==expected
            albums=api.json('/api/photos/albums')['albums']
            assert any(a['path']=='Photos/Trip' and a['title']=='Original album' for a in albums),albums
            done=job(api,'second.zip');assert done['status']=='complete',done
            resumed=job(api,'first.zip');assert resumed['status']=='complete' and resumed['summary']['skipped']==3,resumed
            files=api.json('/api/library')['files']
            assert not any(f['path'].startswith('Google Takeout') for f in files)
            assert any(f['path']=='Trip/two.txt' for f in files)
        finally:api.close()
        print('PASS: reproduce whitespace failure, dry run, encrypted backup, atomic migration, idempotence, stored bytes, album metadata, root-layout resume')
finally:
    subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
