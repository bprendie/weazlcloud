#!/usr/bin/env python3
"""Run from the owner's cron every 20 minutes. Never exposes credentials in logs."""
import argparse
import fcntl
import json
import os
import re
import subprocess
import time
import urllib.error
import zipfile
from pathlib import Path
from support import API, atomic_json, inventory, open_writers, signature, validate, verify


class Watch:
    def __init__(self, cfg):
        self.cfg = cfg
        self.root = Path(cfg['work'])
        self.stage = Path(cfg['stage'])
        self.path = self.root/'state.json'
        self.state = json.loads(self.path.read_text()) if self.path.exists() else {'phase':'waiting','archives':{},'started':time.time()}

    def save(self):
        self.state['updated'] = time.time()
        atomic_json(self.path,self.state)

    def log(self, event, **details):
        print(json.dumps({'time':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),'event':event,**details}),flush=True)

    def waiting(self):
        names = sorted(p.name for p in self.stage.glob(self.cfg['prefix']+'*.zip'))
        snapshot = {name:signature(self.stage/name) for name in names}
        now = time.time()
        if snapshot != self.state.get('observed'):
            self.state['observed'],self.state['stable_since'] = snapshot,now
        total = sum(s[2] for s in snapshot.values())
        writers = open_writers(self.stage)
        self.log('transfer',files=len(names),bytes=total,expected_minimum=self.cfg['minimum_files'],final_present=self.cfg['last_name'] in names,writers=writers)
        ready = len(names)>=self.cfg['minimum_files'] and self.cfg['last_name'] in names and not writers and now-self.state.get('stable_since',now)>=1190
        groups = {}
        for name in names:
            match = re.fullmatch(re.escape(self.cfg['prefix'])+r'(\d+)-(\d+)\.zip',name)
            if not match:
                ready=False
                continue
            group,part = map(int,match.groups())
            groups.setdefault(group,set()).add(part)
        if not groups or any(parts != set(range(1,max(parts)+1)) for parts in groups.values()):
            ready=False
        if not ready:
            self.save()
            return False
        batch = {}
        for name in names:
            path = self.stage/name
            path.chmod(0o640)
            item = {'signature':signature(path),'status':'pending'}
            try:
                item['inventory']=inventory(path)
            except zipfile.BadZipFile as error:
                item.update(status='corrupt_archive',unreadable_archive=True,error=str(error))
            batch[name]=item
        self.state.update(phase='importing',archives=batch,batch_ready=now)
        self.save()
        self.log('batch_ready',files=len(batch),compressed_bytes=total,expanded_bytes=sum(x.get('inventory',{}).get('bytes',0) for x in batch.values()))
        return True

    def remove_verified(self, name, item):
        if item.get('status')!='verified' or not item.get('verification') or self.state['archives'].get(name) is not item or Path(name).name!=name:
            raise RuntimeError('source removal requires a persisted verification record')
        path = self.stage/name
        if path.exists():
            if signature(path) != item['signature'] or name in open_writers(self.stage):
                raise RuntimeError('refusing to remove changed/open ZIP: '+name)
            path.unlink() # Exact frozen batch member; verification was persisted first.
        item['removed_at']=time.time()
        item['status']='removed'
        self.save()
        self.log('source_removed',name=name,bytes=item['signature'][2])

    def run(self):
        if self.state['phase']=='complete':
            return
        if self.state['phase']=='waiting' and not self.waiting():
            return
        api=API(self.cfg)
        try:
            jobs={j['name']:j for j in api.json('/api/takeout')['jobs']}
            running=[j for j in jobs.values() if j['status']=='running']
            for name,item in self.state['archives'].items():
                if item['status'] in ('removed','corrupt_archive'):
                    continue
                if item['status']=='verified':
                    self.remove_verified(name,item)
                    continue
                job=jobs.get(name)
                if job and job['status']=='running':
                    item['summary']=job['summary']
                    self.save()
                    s=job['summary']
                    self.log('import_progress',name=name,files=s['imported']+s['skipped'],total=s['files'],bytes=s['processed_bytes'],corrupt=s.get('corrupt',0))
                    return
                if job and job['status']=='failed':
                    item['summary']=job['summary']
                    raise RuntimeError('import failed; source retained: '+name+': '+job.get('error','unknown error'))
                path=self.stage/name
                if job and job['status']=='complete':
                    item['summary']=job['summary']
                    item['verification']=verify(path,item['prepared'],job['summary'],api)
                    item['status']='verified'
                    self.save()
                    self.remove_verified(name,item)
                    continue
                if running:
                    self.log('other_import_running',name=running[0]['name'])
                    return
                if signature(path)!=item['signature']:
                    raise RuntimeError('batch source changed: '+name)
                fs=os.statvfs(self.stage)
                limit=fs.f_blocks*fs.f_frsize*97//100
                used=(fs.f_blocks-fs.f_bavail)*fs.f_frsize
                inv=item['inventory']
                needed=inv['bytes']+2*inv['largest']+inv['files']*131072+512*1024*1024
                if used+needed>limit:
                    raise RuntimeError('insufficient safe disk headroom for '+name)
                if 'prepared' not in item:
                    self.log('validating',name=name)
                    item['prepared']=validate(path,self.log)
                    item['status']='prepared'
                    self.save()
                if not inv['files']:
                    item['summary']={'files':0,'imported':0,'skipped':0,'corrupt':0,'bytes':0,'processed_bytes':0}
                    item['verification']={'verified_files':0,'verified_samples':0,'time':time.time()}
                    item['status']='verified'
                    self.save()
                    self.remove_verified(name,item)
                    continue
                result=api.json('/api/takeout',{'name':name,'skip_corrupt':True})
                item['status']='running'
                item['summary']=result['summary']
                self.state.pop('error',None)
                self.save()
                self.log('import_started',name=name,files=inv['files'],bytes=inv['bytes'],known_corrupt=len(item['prepared']['errors']))
                return
            self.finish(api)
        finally:
            api.close()

    def finish(self,api):
        failures=[]
        unreadable=[]
        for name,item in self.state['archives'].items():
            if item.get('unreadable_archive'):
                unreadable.append({'archive':name,'error':item['error']})
            for error in item.get('summary',{}).get('errors',[]):
                failures.append({'archive':name,**error})
        atomic_json(self.root/'corrupt-files.json',{'files':failures,'unreadable_archives':unreadable})
        # Explicit user instruction: after the readable data is verified, clear
        # this batch, including corrupt ZIPs, leaving their audit record.
        for name,item in self.state['archives'].items():
            if item['status']=='corrupt_archive':
                item['verification']={'unreadable_archive_logged':True,'time':time.time()}
                item['status']='verified'
                self.save()
                self.remove_verified(name,item)
        stats=api.json('/api/quota')
        listing=api.json('/api/library')['files']
        assert re.fullmatch('[a-f0-9]+',api.owner_id)
        repo=Path(self.cfg['data'])/'users'/api.owner_id/'library'
        physical=int(subprocess.check_output(['du','-sB1',str(repo)],text=True).split()[0])
        logical=sum(f['size'] for f in listing if not f.get('folder'))
        remaining=sum(p.stat().st_size for p in self.stage.iterdir() if p.is_file())
        report={'owner':self.cfg['owner'],'finished':time.time(),'landed_files':sum(not f.get('folder') for f in listing),'landed_logical_bytes':logical,'unique_file_content_bytes':stats.get('unique_bytes'),'dedupe_saved_bytes':max(0,logical-stats.get('unique_bytes',logical)),'dedupe_percent':stats.get('dedupe_percent'),'physical_repository_bytes':physical,'skipped_corrupt_files':len(failures),'unreadable_archives':unreadable,'source_zip_bytes_removed':sum(i['signature'][2] for i in self.state['archives'].values()),'staging_bytes_remaining':remaining,'disk':stats,'archives':self.state['archives']}
        atomic_json(self.root/'report.json',report)
        def gib(n): return f'{n/(1024**3):,.2f} GiB'
        text=f"# bobp Takeout import completed\n\nLanded: {report['landed_files']:,} files, {gib(logical)} logical data.\n\nDedupe: {report['dedupe_percent']}% ({gib(report['dedupe_saved_bytes'])} identical-file savings).\n\nPhysical library repository: {gib(physical)} (includes Restic compression, chunk dedupe and repository metadata).\n\nCorrupt entries skipped: {len(failures)}. Unreadable archives skipped: {len(unreadable)}. Details: corrupt-files.json.\n\nStaging freed: {gib(report['source_zip_bytes_removed'])}. Remaining staging files: {gib(remaining)}.\n\nWindows source files were not changed. Full per-archive audit: report.json.\n"
        out=self.root/'report.md'
        out.write_text(text)
        out.chmod(0o600)
        self.state.update(phase='complete',finished=report['finished'])
        self.state.pop('error',None)
        self.save()
        self.log('complete',files=report['landed_files'],logical_bytes=logical,physical_bytes=physical,dedupe_percent=report['dedupe_percent'],corrupt_files=len(failures))


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--config',required=True)
    args=parser.parse_args()
    os.umask(0o077)
    cfg=json.loads(Path(args.config).read_text())
    root=Path(cfg['work'])
    root.mkdir(mode=0o700,parents=True,exist_ok=True)
    with (root/'watch.lock').open('a') as lock:
        try:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError:
            return
        watcher=Watch(cfg)
        try:
            watcher.run()
        except Exception as error:
            message=str(error)
            if isinstance(error,urllib.error.HTTPError):
                message+=' '+error.read(2048).decode(errors='replace')
            watcher.state['error']=message
            watcher.save()
            watcher.log('needs_attention',error=message)
            raise SystemExit(1)


if __name__=='__main__':
    main()
