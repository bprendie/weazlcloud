#!/usr/bin/env python3
"""Disposable prior-image rollback drill; requires both images already built.
WEAZLCLOUD_ROLLBACK_IMAGE=weazlcloud:previous python3 scripts/smoke-photo-rollback.py
Never points at a configured production volume or address.
"""
import base64
import hashlib
import http.cookiejar
import json
import os
import subprocess
import time
import urllib.request
import uuid

new_image = os.environ.get('WEAZLCLOUD_IMAGE', 'weazlcloud:smoke')
old_image = os.environ['WEAZLCLOUD_ROLLBACK_IMAGE']
backend = os.environ.get('WEAZLCLOUD_SMOKE_STORAGE_BACKEND', 'restic')
name = 'weazl-rollback-' + uuid.uuid4().hex[:10]
volume = name + '-data'
port = int(os.environ.get('WEAZLCLOUD_ROLLBACK_PORT', '29472'))
base = f'http://127.0.0.1:{port}'
password = 'disposable-rollback-fixture'
client = None
current_image = None
png = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=')


def docker(*args):
    return subprocess.check_output(['docker', *args], text=True)


def req(path, data=None, method=None):
    headers = {'X-Weazl-Desk': '1'}
    if data is not None:
        headers['Content-Type'] = 'application/octet-stream' if isinstance(data, bytes) else 'application/json'
        if not isinstance(data, bytes):
            data = json.dumps(data).encode()
    request = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    with client.open(request, timeout=60) as response:
        raw = response.read()
        return json.loads(raw) if 'json' in response.headers.get('Content-Type', '') else raw


def start(image, bootstrap=False):
    global client, current_image
    current_image = image
    docker('run', '-d', '--name', name, '--cpus', '2', '--memory', '4g',
           '-p', f'127.0.0.1:{port}:7272', '-v', volume + ':/data',
           '-e', 'WEAZLCLOUD_DATA=/data', '-e', 'WEAZLCLOUD_DESK_ADDR=:7272',
           '-e', 'WEAZLCLOUD_SHARE_ADDR=:7273', '-e', 'WEAZLCLOUD_DRIVE_ADDR=:7274',
           '-e', f'WEAZLCLOUD_STORAGE_BACKEND={backend}', image)
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    for _ in range(120):
        try:
            req('/ready')
            break
        except Exception:
            time.sleep(.25)
    else:
        raise AssertionError('fixture did not become ready')
    if bootstrap:
        req('/api/bootstrap', {'username': 'rollback', 'password': password,
                              'vault_passphrase': password, 'confirm': password})
    else:
        req('/api/login', {'username': 'rollback', 'password': password})
    if not req('/api/status').get('unlocked'):
        req('/api/unlock', {'passphrase': password})


def stop():
    stream = client.open(base + '/api/library/events', timeout=60) if current_image == new_image else None
    docker('stop', '-t', '120', name)
    state = json.loads(docker('inspect', name))[0]['State']
    assert state['ExitCode'] == 0 and not state['OOMKilled'], state
    if stream:
        stream.close()
        docker('run', '--rm', '--network', 'none', '--entrypoint', '/bin/sh',
               '-v', volume + ':/data:ro', new_image, '-c',
               'for f in /data/users/*/.weazl-photo-jobs.journal.enc; do test -f "$f" && test ! -s "$f" || exit 1; done')
    docker('rm', name)


def prepare():
    req('/api/photos/preparation', {'action': 'resume'})
    for _ in range(240):
        state = req('/api/photos/preparation')
        if state['status'] in ('complete', 'partial', 'paused_error'):
            break
        time.sleep(.25)
    assert state['status'] == 'complete' and state['ready'] == 2, state
    req('/api/photos/preparation', {'action': 'pause'})
    return state


def inventory():
    files = req('/api/library')['files']
    photos = req('/api/v1/photos?limit=10')['items']
    identities = sorted((p['id'], p['path'], p['size']) for p in photos)
    originals = sorted((p['id'], hashlib.sha256(req('/api/v1/photos/assets/' + p['id'] + '/original')).hexdigest()) for p in photos)
    images = sorted((p['id'], size, hashlib.sha256(req('/api/v1/photos/assets/' + p['id'] + '/thumbnail?size=' + str(size))).hexdigest()) for p in photos for size in (320, 1280))
    return sorted((f['path'], f.get('size')) for f in files), identities, originals, images


try:
    start(new_image, True)
    for path in ('Photos/first.png', 'Photos/second.png'):
        req('/api/library?path=' + path, png, 'PUT')
    state = prepare()
    assert state['bundle_ready'] == 2, state
    before = inventory()
    stop()  # Clean unlocked drain exports the journal into the v1 snapshot.
    start(old_image)
    assert req('/api/photos/preparation')['paused']
    assert inventory() == before, 'old image changed identity/original/derivative bytes'
    prepare()  # Exercise the previous image's queue reader/writer, using warm cache.
    stop()
    start(new_image)
    assert req('/api/photos/preparation')['paused']
    assert inventory() == before, 'forward restart after rollback changed bytes'
    assert prepare()['bundle_ready'] == 2
    stop()
    print(f'PASS ({backend}): clean journal export -> prior image -> new image; pause, IDs, originals and both preview variants preserved')
finally:
    subprocess.run(['docker', 'rm', '-f', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.run(['docker', 'volume', 'rm', volume], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
