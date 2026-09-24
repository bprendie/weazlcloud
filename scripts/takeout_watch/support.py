"""Private, resumable server-side Takeout batch operations (Python stdlib only)."""
import hashlib
import http.cookies
import json
import os
import posixpath
import re
import stat
import time
import urllib.parse
import urllib.request
import zipfile
import zlib
from pathlib import Path


def atomic_json(path, value):
    path = Path(path)
    tmp = path.with_suffix('.tmp')
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w') as out:
        json.dump(value, out, indent=2)
        out.flush()
        os.fsync(out.fileno())
    os.replace(tmp, path)


def signature(path):
    s = path.lstat()
    if not stat.S_ISREG(s.st_mode):
        raise ValueError('source is not a regular file: '+path.name)
    return [s.st_dev, s.st_ino, s.st_size, s.st_mtime_ns]


def open_writers(stage):
    found = []
    for proc in Path('/proc').iterdir():
        try:
            if not proc.name.isdecimal() or proc.stat().st_uid != os.getuid():
                continue
            for fd in (proc/'fd').iterdir():
                target = os.readlink(fd)
                if not target.startswith(str(stage)+'/'):
                    continue
                info = (proc/'fdinfo'/fd.name).read_text()
                flags = re.search(r'^flags:\s+(\d+)', info, re.M)
                if flags and int(flags[1], 8) & os.O_ACCMODE:
                    found.append(Path(target).name)
        except (OSError, ValueError):
            continue
    return sorted(set(found))


def destination(name):
    parts = name.rstrip('/').split('/')
    if not name or name.startswith('/') or '\\' in name or '\0' in name or any(p in ('', '.', '..') or ':' in p for p in parts):
        raise ValueError('unsafe ZIP entry: '+name)
    if len(parts) > 1 and parts[0].lower() == 'takeout':
        parts = parts[1:]
    group = {'drive':'Drive', 'google drive':'Drive', 'photos':'Photos', 'google photos':'Photos'}.get(parts[0].lower(), 'Other')
    if group != 'Other':
        parts = parts[1:]
    return posixpath.normpath(('/'.join(['Google Takeout',group]+parts)).strip())


def digest(reader):
    h = hashlib.sha256()
    size = 0
    while chunk := reader.read(1024*1024):
        h.update(chunk)
        size += len(chunk)
    return h.hexdigest(), size


class API:
    def __init__(self, cfg):
        self.base = cfg['api']
        # Credential-bearing HTTP is restricted to this host's loopback listener.
        assert self.base == 'http://127.0.0.1:7272'
        text = Path(cfg['credentials']).read_text()
        fields = {k.lower():v.strip().strip('`') for k,v in re.findall(r'(?im)^[-* ]*(Username|Password)[*: ]+([^\n]+)',text)}
        assert fields['username'] == cfg['owner'] == 'bobp'
        self.cookie = ''
        with self.open('/api/login', {'username':cfg['owner'],'password':fields['password']}) as response:
            cookie = http.cookies.SimpleCookie()
            cookie.load(response.headers['Set-Cookie'])
            self.cookie = 'weazl_session='+cookie['weazl_session'].value
            self.owner_id = json.load(response)['id']
        self.json('/api/unlock', {'passphrase':fields['password']})

    def open(self, path, data=None):
        request = urllib.request.Request(self.base+path, data=None if data is None else json.dumps(data).encode(), headers={'X-Weazl-Desk':'1','Content-Type':'application/json','Cookie':self.cookie})
        return urllib.request.urlopen(request, timeout=1800)

    def json(self, path, data=None):
        with self.open(path, data) as response:
            return json.load(response)

    def close(self):
        try:
            self.json('/api/logout', {})
        except Exception:
            pass


def inventory(path):
    with zipfile.ZipFile(path) as z:
        files = [f for f in z.infolist() if not f.is_dir()]
        for f in z.infolist():
            destination(f.filename)
            mode = (f.external_attr >> 16) & 0o170000
            if mode not in (0, stat.S_IFREG, stat.S_IFDIR):
                raise ValueError('special ZIP entry: '+f.filename)
        return {'files':len(files),'bytes':sum(f.file_size for f in files),'largest':max((f.file_size for f in files),default=0)}


def validate(path, log):
    original = signature(path)
    with path.open('rb') as src:
        sha, _ = digest(src)
    result = {'source_signature':original,'source_sha256':sha,'errors':[],'samples':[]}
    with zipfile.ZipFile(path) as z:
        entries = [f for f in z.infolist() if not f.is_dir()]
        candidates = [f for f in entries if f.file_size <= 512*1024*1024]
        samples = set()
        if candidates:
            samples = {candidates[i].filename for i in (0,len(candidates)//2,len(candidates)-1)}
            samples.add(max(candidates,key=lambda f:f.file_size).filename)
        last = time.time()
        for n, entry in enumerate(entries):
            try:
                with z.open(entry) as reader:
                    checksum, size = digest(reader) # Reading to EOF checks the ZIP CRC.
                if size != entry.file_size:
                    raise zipfile.BadZipFile('expanded size mismatch')
                if entry.filename in samples:
                    result['samples'].append({'path':destination(entry.filename),'sha256':checksum,'bytes':size})
            except (zipfile.BadZipFile, EOFError, zlib.error, NotImplementedError) as error:
                result['errors'].append({'path':entry.filename,'error':str(error),'bytes':entry.file_size})
            if time.time()-last >= 1200:
                log('validating', name=path.name, checked=n+1, total=len(entries), corrupt=len(result['errors']))
                last=time.time()
    if signature(path) != original:
        raise RuntimeError('source changed during validation: '+path.name)
    return result


def verify(path, prepared, summary, api):
    if signature(path) != prepared['source_signature']:
        raise RuntimeError('source changed during import: '+path.name)
    errors = {item['path'] for item in summary.get('errors',[])}
    preflight_errors = {item['path'] for item in prepared['errors']}
    if not preflight_errors.issubset(errors):
        raise RuntimeError('preflight and importer corruption reports disagree')
    if summary['imported']+summary['skipped']+summary.get('corrupt',0) != summary['files'] or summary['processed_bytes'] != summary['bytes']:
        raise RuntimeError('incomplete import accounting')
    library = {f['path']:f for f in api.json('/api/library')['files']}
    checked = 0
    with zipfile.ZipFile(path) as z:
        for f in z.infolist():
            if f.is_dir() or f.filename in errors:
                continue
            stored = library.get(destination(f.filename))
            if not stored or stored.get('folder') or stored['size'] != f.file_size:
                raise RuntimeError('library entry missing or wrong size: '+f.filename)
            checked += 1
    for sample in prepared['samples']:
        with api.open('/api/library?path='+urllib.parse.quote(sample['path'],safe='')) as stream:
            sha, size = digest(stream)
        if sha != sample['sha256'] or size != sample['bytes']:
            raise RuntimeError('stored file hash mismatch: '+sample['path'])
    return {'verified_files':checked,'verified_samples':len(prepared['samples']),'time':time.time()}
