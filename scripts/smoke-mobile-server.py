#!/usr/bin/env python3
"""Native API smoke using disposable local Docker fixtures, never a supplied URL.

Build weazlcloud:smoke first. Python's standard library and a local Docker daemon
are sufficient. Both restic and shared-experimental run by default. The native
parts/router contract must be wired before this smoke is run or enabled in CI.
"""
import argparse
import base64
import hashlib
import http.cookiejar
import io
import json
import os
import re
import secrets
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import zipfile

PART_SIZE = 16 << 20
PNG = base64.b64decode(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+'
    'A8AAQUBAScY42YAAAAASUVORK5CYII=')
SCOPES = ['photos:read', 'photos:write', 'files:read', 'files:write',
          'backup:write', 'grabs:read', 'grabs:write', 'storage:read']


class SmokeFailure(Exception):
    """Messages contain only fixed stage labels and status codes, never bodies."""


def require(condition, message):
    if not condition:
        raise SmokeFailure(message)


def docker(*args, check=True, timeout=120):
    result = subprocess.run(['docker', *args], capture_output=True, text=True,
                            timeout=timeout)
    if check and result.returncode:
        raise SmokeFailure('local Docker fixture command failed: ' + args[0])
    return result


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


class Client:
    def __init__(self, origin, token=None, cookies=False):
        parsed = urllib.parse.urlsplit(origin)
        require(parsed.scheme == 'http' and parsed.hostname == '127.0.0.1'
                and parsed.port and not parsed.path, 'nonlocal fixture origin')
        self.origin, self.token = origin, token
        handlers = [urllib.request.ProxyHandler({}), NoRedirect()]
        if cookies:
            handlers.append(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.opener = urllib.request.build_opener(*handlers)
        self.stage = 'request'

    def request(self, method, path, body=None, headers=None, expected=(200,), timeout=60, sink=None):
        require(path.startswith('/') and not path.startswith('//'), 'invalid fixture path')
        fields = {'X-Weazl-Desk': '1'}
        if self.token:
            fields['Authorization'] = 'Bearer ' + self.token
        if isinstance(body, (dict, list)):
            body = json.dumps(body, separators=(',', ':')).encode()
            fields['Content-Type'] = 'application/json'
        if body is not None:
            fields['Content-Length'] = str(len(body))
        fields.update(headers or {})
        req = urllib.request.Request(self.origin + path, data=body,
                                     headers=fields, method=method)
        try:
            response = self.opener.open(req, timeout=timeout)
        except urllib.error.HTTPError as error:
            response = error
        except (urllib.error.URLError, TimeoutError, OSError) as error:
            cause = getattr(error, 'reason', error)
            raise SmokeFailure(self.stage + ': request unavailable (' + type(cause).__name__ + ')') from None
        with response:
            status, response_headers = response.code, response.headers
            require(status in expected, self.stage + ': unexpected HTTP ' + str(status))
            if sink is None:
                data = response.read()
            else:
                while block := response.read(1 << 20):
                    sink(block)
                data = b''
        return data, response_headers

    def json(self, method, path, body=None, headers=None, expected=(200,), timeout=60):
        data, _ = self.request(method, path, body, headers, expected, timeout)
        try:
            return json.loads(data)
        except (ValueError, UnicodeError):
            raise SmokeFailure(self.stage + ': invalid JSON response') from None

    def wait_ready(self):
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            try:
                self.request('GET', '/ready', timeout=2)
                return
            except SmokeFailure:
                time.sleep(.25)
        raise SmokeFailure(self.stage + ': fixture startup timed out')


def fixture_origin(name, port):
    binding = docker('port', name, str(port) + '/tcp').stdout.strip()
    require(binding.startswith('127.0.0.1:') and '\n' not in binding,
            'fixture must publish only on loopback')
    return 'http://' + binding


def restart_fixture(name, admin, client, guest=None):
    docker('restart', '--time', '5', name)
    # Docker can assign new ephemeral host ports on restart.
    admin.origin = client.origin = fixture_origin(name, 7272)
    if guest is not None:
        guest.origin = fixture_origin(name, 7273)
    admin.wait_ready()


def verify_local_docker():
    context = os.environ.get('DOCKER_CONTEXT')
    endpoint = None if context else os.environ.get('DOCKER_HOST')
    if not endpoint:
        context_args = [context] if context else []
        endpoint = docker('context', 'inspect', *context_args, '--format',
                          '{{.Endpoints.docker.Host}}').stdout.strip()
    require(endpoint.startswith('unix://'), 'a local Unix-socket Docker daemon is required')


def snapshot(client, prefix=''):
    client.stage = 'Files snapshot'
    rows, cursor, seen = [], '', set()
    for _ in range(100):
        query = {'limit': 2, 'prefix': prefix}
        if cursor:
            query['cursor'] = cursor
        page = client.json('GET', '/api/v1/files/sync?' + urllib.parse.urlencode(query))
        require(not page.get('resync_required'), 'Files snapshot unexpectedly requires reset')
        rows.extend(page['items'])
        if not page['has_more']:
            checkpoint = page['checkpoint']
            client.json('POST', '/api/v1/files/sync/checkpoint',
                        {'checkpoint': checkpoint, 'prefix': prefix})
            return rows, checkpoint
        cursor = page['next_cursor']
        require(cursor not in seen, 'Files snapshot repeated a cursor')
        seen.add(cursor)
    raise SmokeFailure('Files snapshot exceeded its page bound')


def transfer_create(client, route, spec):
    created = client.json('POST', route, spec, expected=(200, 201))
    receipt = created['receipt']
    logical_id = receipt['upload']['id'] if 'upload' in receipt else receipt['id']
    transfer = created.get('transfer')
    if transfer is None:
        require(receipt['status'] == 'stored', 'creation omitted an unfinished transfer')
        transfer = client.json('GET', route + '/' + logical_id)['transfer']
    require(transfer['id'] == logical_id and transfer['transport'] == 'parts-v1'
            and transfer['part_size'] == PART_SIZE, 'parts creation contract mismatch')
    return transfer


def transfer_parts(client, route, transfer, payload):
    require(len(payload) > PART_SIZE, 'reordered fixture requires two parts')
    upload = route + '/' + transfer['id']
    tail = payload[PART_SIZE:]
    endpoint = upload + '/components/original/parts/1'
    fields = {'Content-Type': 'application/octet-stream',
              'X-Weazl-SHA256': hashlib.sha256(tail).hexdigest()}
    for _ in range(2):
        client.request('PUT', endpoint, tail, fields, expected=(200, 201, 202, 204))
    progress = client.json('GET', upload)['transfer']
    component = next(c for c in progress['components'] if c['id'] == 'original')
    require(component['received_parts'] == 1 and component['received_bytes'] == len(tail),
            'repeated part advanced accepted progress')
    first = payload[:PART_SIZE]
    client.request('PUT', upload + '/components/original/parts/0', first,
                   {'Content-Type': 'application/octet-stream',
                    'X-Weazl-SHA256': hashlib.sha256(first).hexdigest()},
                   expected=(200, 201, 202, 204))
    # No finalize request: completing the parts must durably enqueue the commit.
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        view = client.json('GET', upload, timeout=10)['transfer']
        require(view['status'] != 'failed', 'parts background commit failed')
        if view['status'] == 'stored':
            result = view['result']
            require(result['status'] == 'stored', 'transfer reported stored without receipt')
            return result
        time.sleep(.25)
    raise SmokeFailure('parts background commit timed out')


def photo_spec(device_id, album_id, payload, hidden=False, opaque=False):
    return {'device_id': device_id, 'device_asset_id': 'opaque' if opaque else 'photo',
            'source_revision': '1', 'transport': 'parts-v1', 'commit_when_complete': True,
            'hidden': hidden, 'original_mode': 'opaque-original-v1' if opaque else '',
            'album_ids': [album_id], 'components': [{'id': 'original',
            'filename': 'unknown.camera' if opaque else 'fixture.png',
            'size': len(payload), 'sha256': hashlib.sha256(payload).hexdigest()}]}


def collections(client):
    client.stage = 'nested collections'
    route = '/api/v1/photos/collections'
    root = client.json('POST', route, {'action': 'save-folder', 'folder': {'title': 'Phone'}})
    nested = client.json('POST', route, {'action': 'save-folder',
                        'folder': {'title': 'Trips', 'parent_id': root['id']}})
    album = client.json('POST', route, {'action': 'save-album', 'parent_id': nested['id'],
                        'album': {'title': 'Fixture album'}})
    empty = client.json('POST', route, {'action': 'save-album', 'parent_id': nested['id'],
                        'album': {'title': 'Empty album'}})
    page = client.json('GET', route + '?limit=200')
    nodes = {node['id']: node for node in page['nodes']}
    require(nodes[nested['id']]['folder']['parent_id'] == root['id']
            and nodes[album['id']]['album']['parent_id'] == nested['id']
            and empty['id'] in nodes, 'nested or empty collection was lost')
    return album


def file_backup(admin, client, device_id, payload):
    client.stage = admin.stage = 'file source and parts'
    admin.json('POST', '/api/library/folder', {'path': 'Backups/Phone/Fixture'}, expected=(200, 201))
    rows, _ = snapshot(client, 'Backups')
    destination = next(row for row in rows if row['path'] == 'Backups/Phone/Fixture')
    client.json('POST', '/api/v1/backups/sources',
                {'device_id': device_id, 'source_id': 'fixture-source',
                 'destination_id': destination['id'], 'name': 'Fixture'}, expected=(200, 201))
    spec = {'device_id': device_id, 'source_id': 'fixture-source',
            'source_item_id': 'fixture-file', 'source_revision': '1',
            'relative_path': 'native.bin', 'kind': 'file', 'size': len(payload),
            'sha256': hashlib.sha256(payload).hexdigest(), 'mtime': '2026-10-02T12:00:00Z',
            'transport': 'parts-v1', 'commit_when_complete': True}
    route = '/api/v1/backups/uploads'
    transfer = transfer_create(client, route, spec)
    result = transfer_parts(client, route, transfer, payload)
    empty_folder = dict(spec, source_item_id='fixture-empty', relative_path='empty',
                        kind='folder', size=0, sha256='')
    empty = client.json('POST', route, empty_folder, expected=(200, 201))['receipt']
    require(empty['status'] == 'stored' and empty['file']['folder'],
            'empty backup folder did not commit immediately')
    return spec, transfer, result['file']


def file_reads(client, file, payload):
    client.stage = 'Files content and Range'
    rows, _ = snapshot(client, 'Backups')
    row = next(row for row in rows if row['id'] == file['entry_id'])
    require(row['revision'] > 0 and row['size'] == len(payload), 'Files metadata mismatch')
    require(any(row['path'] == 'Backups/Phone/Fixture/empty' and row['folder'] for row in rows),
            'empty folder missing from Files snapshot')
    route = '/api/v1/files/' + row['id'] + '/content'
    _, headers = client.request('HEAD', route)
    etag = headers.get('ETag')
    require(etag and headers.get('Cache-Control') == 'private, no-store'
            and int(headers['Content-Length']) == len(payload), 'Files HEAD contract mismatch')
    start, end = PART_SIZE - 16, PART_SIZE + 31
    got, headers = client.request('GET', route, headers={'Range': f'bytes={start}-{end}',
                                  'If-Range': etag}, expected=(206,))
    require(got == payload[start:end+1]
            and headers['Content-Range'] == f'bytes {start}-{end}/{len(payload)}',
            'range resume returned different bytes')
    data, _ = client.request('GET', route)
    require(hashlib.sha256(data).digest() == hashlib.sha256(payload).digest(),
            'file download hash mismatch')


def mint_grabs(client, photo, file, album):
    client.stage = 'idempotent grabs'
    selection = client.json('POST', '/api/v1/photos/selections',
                            {'mode': 'all', 'filter': {'album': 'album:' + album['id']}},
                            expected=(201,))
    require(selection['count'] == 1, 'album selection has unexpected membership')
    cases = [('/api/capsules', {'path': file['path'], 'kind': 'file'}, 200),
             ('/api/capsules', {'path': 'Backups/Phone/Fixture', 'kind': 'folder'}, 200),
             ('/api/v1/photos/grabs', {'selection_id': selection['id'], 'title': 'Fixture'}, 201)]
    operations = []
    for route, body, status in cases:
        body.update(gate='open', grabs=3, expiry='24h')
        fields = {'Idempotency-Key': secrets.token_urlsafe(24)}
        original = client.json('POST', route, body, fields, expected=(status,))
        replay = client.json('POST', route, body, fields, expected=(status,))
        require(original == replay and original['state'] == 'ready'
                and original['url'] == 'https://grab.mobile.test/g/' + original['id']
                and original['left'] == 3, 'grab retry changed the original result')
        conflict = dict(body, grabs=4)
        client.request('POST', route, conflict, fields, expected=(409,))
        client.json('GET', '/api/v1/grabs/operations', headers=fields, expected=(status,))
        operations.append((route, body, fields, status, original))
    listed = client.json('GET', '/api/capsules')['capsules']
    require(len(listed) == len(cases) and {c['id'] for c in listed}
            == {op[4]['id'] for op in operations}, 'duplicate owner capsule listing')
    return operations


def extended_photos(client, device_id, album_id):
    client.stage = 'optional hidden opaque original'
    payload = b'opaque original\x00' + bytes(range(256))
    spec = photo_spec(device_id, album_id, payload, hidden=True, opaque=True)
    route = '/api/v1/photos/uploads'
    transfer = transfer_create(client, route, spec)
    client.request('PUT', route + '/' + transfer['id'] + '/components/original/parts/0',
                   payload, {'X-Weazl-SHA256': hashlib.sha256(payload).hexdigest()},
                   expected=(200, 201, 202, 204))
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        stored = client.json('GET', route + '/' + transfer['id'])['transfer']
        require(stored['status'] != 'failed', 'optional photo commit failed')
        if stored['status'] == 'stored':
            photo = stored['result']
            break
        time.sleep(.25)
    else:
        raise SmokeFailure('optional photo commit timed out')
    body = {'ids': [photo['asset_id']], 'hidden': True, 'gate': 'open', 'grabs': 2}
    client.request('POST', '/api/v1/photos/grabs', body,
                   {'Idempotency-Key': secrets.token_urlsafe(24)}, expected=(400,))
    body['confirm_hidden'] = True
    result = client.json('POST', '/api/v1/photos/grabs', body,
                         {'Idempotency-Key': secrets.token_urlsafe(24)}, expected=(201,))
    client.json('DELETE', '/api/capsules?' + urllib.parse.urlencode({'id': result['id']}))


class MemorySamples:
    """Sample PID 1 RSS; record cgroup-v2 anonymous and kernel peak if available."""
    def __init__(self, name):
        self.name, self.stop = name, threading.Event()
        self.samples, self.baseline, self.peak_rss = 0, None, 0
        self.peak_anon, self.cgroup_peak = 0, 0
        self.thread = threading.Thread(target=self.run, daemon=True)

    def sample(self):
        try:
            raw = docker('exec', self.name, 'cat', '/proc/1/status',
                         '/sys/fs/cgroup/memory.stat', '/sys/fs/cgroup/memory.peak',
                         check=False, timeout=5).stdout
        except (OSError, subprocess.TimeoutExpired):
            return
        for line in raw.splitlines():
            fields = line.split()
            if len(fields) >= 2 and fields[0] == 'VmRSS:':
                rss = int(fields[1]) * 1024
                if self.baseline is None:
                    self.baseline = rss
                self.peak_rss = max(self.peak_rss, rss)
                self.samples += 1
            elif len(fields) == 2 and fields[0] == 'anon':
                self.peak_anon = max(self.peak_anon, int(fields[1]))
            elif len(fields) == 1 and fields[0].isdigit():
                self.cgroup_peak = max(self.cgroup_peak, int(fields[0]))

    def run(self):
        while not self.stop.wait(.25):
            self.sample()

    def __enter__(self):
        self.sample()
        self.thread.start()
        return self

    def __exit__(self, *args):
        self.stop.set()
        self.thread.join(timeout=6)
        self.sample()


def staging_disk(name):
    # Inspect only disposable fixture roots. du output/filenames stay inside the
    # container; stdout contains bounded numeric totals, never private paths.
    script = r'''
set -eu
allocated=0; payloads=0; receipts=0; unexpected=0; maximum=0; files=0; roots=0
for root in /data/users/*/.weazl-mobile-parts; do
    [ -d "$root" ] || continue
    roots=$((roots + 1)); [ "$roots" -le 4 ] || exit 2
    kb=$(du -sk "$root" | awk '{print $1}')
    allocated=$((allocated + kb * 1024))
    for file in "$root"/*/* "$root"/*/.[!.]* "$root"/.live/* "$root"/.queue/* "$root"/.indexed-v1; do
        [ -e "$file" ] || continue
        files=$((files + 1)); [ "$files" -le 256 ] || exit 2
        if [ ! -f "$file" ] || [ -L "$file" ]; then
            unexpected=$((unexpected + 1)); continue
        fi
        bytes=$(wc -c < "$file")
        case "$file" in
            *.wza)
                payloads=$((payloads + 1))
                [ "$(head -c 4 "$file")" = WZA1 ] || unexpected=$((unexpected + 1));;
            *.enc)
                receipts=$((receipts + 1))
                [ "$bytes" -le 262144 ] || unexpected=$((unexpected + 1))
                [ "$bytes" -le "$maximum" ] || maximum=$bytes;;
            *) [ "$bytes" -eq 0 ] || unexpected=$((unexpected + 1));;
        esac
    done
done
[ "$roots" -gt 0 ] || exit 2
printf '%s %s %s %s %s\n' "$allocated" "$payloads" "$receipts" "$unexpected" "$maximum"
'''
    result = docker('exec', name, 'sh', '-c', script, timeout=10).stdout.strip()
    require(re.fullmatch(r'\d+ \d+ \d+ \d+ \d+', result), 'invalid staging disk measurement')
    return tuple(map(int, result.split()))


def large_stream(admin, client, name, backend, backup_spec, mib):
    client.stage = admin.stage = 'large stream and staging restart'
    size, block = mib << 20, bytes(range(256)) * 4096
    digest = hashlib.sha256()
    with tempfile.TemporaryFile(prefix='weazl-mobile-fixture-') as fixture:
        for _ in range(mib):
            fixture.write(block)
            digest.update(block)
        fixture.flush()
        spec = dict(backup_spec, source_item_id='fixture-large', relative_path='stream.bin',
                    size=size, sha256=digest.hexdigest())
        route = '/api/v1/backups/uploads'
        started = time.monotonic()
        with MemorySamples(name) as memory:
            client.stage = 'large transfer create'
            transfer = transfer_create(client, route, spec)
            upload = route + '/' + transfer['id']
            count = (size + PART_SIZE - 1) // PART_SIZE

            def part(index):
                client.stage = 'large transfer part ' + str(index)
                fixture.seek(index * PART_SIZE)
                data = fixture.read(min(PART_SIZE, size - index * PART_SIZE))
                client.request('PUT', upload + f'/components/original/parts/{index}', data,
                               {'Content-Type': 'application/octet-stream',
                                'X-Weazl-SHA256': hashlib.sha256(data).hexdigest()},
                               expected=(200, 201, 202, 204))

            part(count - 1)
            print(f'MOBILE ({backend}): large tail part accepted; repeating it', flush=True)
            part(count - 1)
            part(1)
            admin.stage = 'large staging restart'
            restart_fixture(name, admin, client)
            admin.json('POST', '/api/login', client.restart_login)
            admin.json('POST', '/api/unlock', client.restart_unlock)
            recovered = client.json('GET', upload)['transfer']
            component = next(c for c in recovered['components'] if c['id'] == 'original')
            require(component['received_parts'] == 2, 'restart lost or duplicated staged parts')
            for index in reversed(range(count - 1)):
                if index != 1:
                    if index == 0:
                        staged = staging_disk(name)
                        require(staged[1] == count - 1 and staged[3] == 0,
                                'staging contains unexpected or unencrypted payloads')
                        require(size - PART_SIZE <= staged[0] <= size - PART_SIZE + (4 << 20),
                                'staging disk use exceeds encrypted parts bound')
                    part(index)
                if index % 4 == 0:
                    print(f'MOBILE ({backend}): large fixture parts progress', flush=True)
            deadline = time.monotonic() + 180
            client.stage = 'large auto commit'
            while time.monotonic() < deadline:
                stored = client.json('GET', upload, timeout=10)['transfer']
                require(stored['status'] != 'failed', 'large stream background commit failed')
                if stored['status'] == 'stored':
                    break
                time.sleep(.25)
            else:
                raise SmokeFailure('large stream background commit timed out')
            deadline = time.monotonic() + 10
            while True:
                cleaned = staging_disk(name)
                require(cleaned[3] == 0, 'unexpected assembled or plaintext staging file')
                if cleaned[1] == 0:
                    break
                require(time.monotonic() < deadline, 'stored transfer retained payload staging')
                time.sleep(.25)
            require(cleaned[0] < 1 << 20 and cleaned[2] > 0,
                    'stored staging did not shrink to small receipt directories')
            file = stored['result']['file']
            downloaded, received = hashlib.sha256(), 0

            def consume(data):
                nonlocal received
                downloaded.update(data)
                received += len(data)

            client.stage = 'large streaming content read'
            client.request('GET', '/api/v1/files/' + file['entry_id'] + '/content',
                           timeout=180, sink=consume)
            require(received == size and downloaded.hexdigest() == spec['sha256'],
                    'large stream original hash mismatch')
        require(memory.samples > 0 and memory.baseline is not None, 'server RSS sampling unavailable')
        elapsed = time.monotonic() - started
        unit = 1 << 20
        print(f'METRICS ({backend}): {mib} MiB generated; transfer/restart/read {elapsed:.1f}s; '
              f'{mib/elapsed:.1f} MiB/s end-to-end; server RSS baseline '
              f'{memory.baseline/unit:.1f} MiB, sampled peak {memory.peak_rss/unit:.1f} MiB '
              f'({memory.samples} samples); sampled cgroup anon {memory.peak_anon/unit:.1f} MiB; '
              f'kernel cgroup peak {memory.cgroup_peak/unit:.1f} MiB', flush=True)
        print(f'DISK ({backend}): encrypted parts before final missing part {staged[0]} '
              f'allocated bytes ({staged[1]} payloads); after stored cleanup {cleaned[0]} '
              f'allocated bytes ({cleaned[1]} payloads, {cleaned[2]} encrypted receipts, '
              f'maximum receipt {cleaned[4]} bytes); no assembled/plaintext native-part staging', flush=True)


def run_backend(image, backend, extended, large_mib):
    name = 'weazl-mobile-' + uuid.uuid4().hex[:12]
    volume = name + '-data'
    password, vault_phrase = secrets.token_urlsafe(24), secrets.token_urlsafe(24)
    started = time.monotonic()
    print(f'MOBILE ({backend}): starting isolated fixture', flush=True)
    try:
        docker('run', '--detach', '--pull', 'never', '--name', name,
               '--publish', '127.0.0.1::7272', '--publish', '127.0.0.1::7273',
               '--cpus', os.environ.get('WEAZLCLOUD_SMOKE_CPUS', '2'),
               '--memory', os.environ.get('WEAZLCLOUD_SMOKE_MEMORY', '4g'),
               '--env', 'WEAZLCLOUD_DATA=/data', '--env', 'WEAZLCLOUD_DESK_ADDR=:7272',
               '--env', 'WEAZLCLOUD_SHARE_ADDR=:7273', '--env', 'WEAZLCLOUD_DRIVE_ADDR=:7274',
               '--env', 'WEAZLCLOUD_PUBLIC_BASE=https://grab.mobile.test',
               '--env', 'WEAZLCLOUD_STORAGE_BACKEND=' + backend,
               '--volume', volume + ':/data', image)
        origin = fixture_origin(name, 7272)
        admin, guest = Client(origin, cookies=True), Client(fixture_origin(name, 7273))
        admin.wait_ready()
        admin.stage = 'owner bootstrap'
        admin.json('POST', '/api/bootstrap', {'username': 'mobile-smoke', 'password': password,
                   'vault_passphrase': vault_phrase, 'confirm': vault_phrase}, expected=(201,))
        admin.json('POST', '/api/unlock', {'passphrase': vault_phrase})
        admin.json('POST', '/api/node', {'hostname': 'grab.mobile.test'})
        enrolled = admin.json('POST', '/api/v1/devices', {'name': 'Smoke phone',
                              'scopes': SCOPES}, expected=(201,))
        require(set(enrolled['scopes']) == set(SCOPES), 'device grants mismatch')
        device_id = enrolled['device']['id']
        client = Client(origin, enrolled['token'])
        client.restart_login = {'username': 'mobile-smoke', 'password': password}
        client.restart_unlock = {'passphrase': vault_phrase}
        album = collections(client)
        photo_payload = PNG + bytes(PART_SIZE + 1024 - len(PNG))
        client.stage = 'photo reordered parts and auto finalize'
        spec = photo_spec(device_id, album['id'], photo_payload)
        photo_transfer = transfer_create(client, '/api/v1/photos/uploads', spec)
        photo = transfer_parts(client, '/api/v1/photos/uploads', photo_transfer, photo_payload)
        original, _ = client.request('GET', '/api/v1/photos/assets/' + photo['asset_id'] + '/original')
        require(original == photo_payload, 'photo original hash mismatch')
        lookup_body = {'items': [{'sha256': hashlib.sha256(photo_payload).hexdigest(),
                                  'size': len(photo_payload)},
                                 {'sha256': hashlib.sha256(photo_payload).hexdigest(),
                                  'size': len(photo_payload) + 1}]}
        def check_lookup():
            results = client.json('POST', '/api/v1/photos/lookup', lookup_body)['results']
            require(results[0]['exists'] and not results[1]['exists'], 'content lookup size mismatch')
            require(results[0]['matches'][0]['asset_id'] == photo['asset_id'],
                    'content lookup lost uploaded photo identity')
        check_lookup()
        capabilities = client.json('GET', '/api/v1/mobile/capabilities')
        require(capabilities['features']['photo_content_lookup'], 'lookup discovery missing')
        require(capabilities['limits']['photo_lookup_items'] == 200, 'lookup batch limit missing')
        print(f'MOBILE ({backend}): photo auto commit verified', flush=True)
        file_payload = bytes(range(256)) * (PART_SIZE // 256 + 8)
        backup_spec, file_transfer, file = file_backup(admin, client, device_id, file_payload)
        file_reads(client, file, file_payload)
        operations = mint_grabs(client, photo, file, album)
        print(f'MOBILE ({backend}): file auto commit, Range and idempotent grabs verified', flush=True)
        if extended:
            extended_photos(client, device_id, album['id'])
            print(f'MOBILE ({backend}): Hidden opaque original and sharing confirmation verified', flush=True)
        admin.stage = 'restart login and unlock'
        restart_fixture(name, admin, client, guest)
        admin.json('POST', '/api/login', {'username': 'mobile-smoke', 'password': password})
        admin.json('POST', '/api/unlock', {'passphrase': vault_phrase})
        check_lookup()
        client.stage = 'durable receipts after restart'
        for route, upload_spec, transfer in [('/api/v1/photos/uploads', spec, photo_transfer),
                                            ('/api/v1/backups/uploads', backup_spec, file_transfer)]:
            replay = transfer_create(client, route, upload_spec)
            require(replay['id'] == transfer['id'] and replay['status'] == 'stored',
                    'restart created another upload receipt')
        client.stage = guest.stage = 'grab recovery and revocation'
        for route, body, fields, status, original in operations:
            replay = client.json('POST', route, body, fields, expected=(status,))
            require(replay == original, 'restart created another capsule')
            if body.get('kind') in ('file', 'folder'):
                data, _ = guest.request('POST', '/g/' + original['id'] + '/file', {})
                if body['kind'] == 'folder':
                    with zipfile.ZipFile(io.BytesIO(data)) as archive:
                        data = archive.read('native.bin')
                        require(archive.getinfo('empty/').is_dir(),
                                'frozen folder grab lost its empty folder')
                require(data == file_payload, 'frozen grab bytes changed')
            client.json('DELETE', '/api/capsules?' + urllib.parse.urlencode({'id': original['id']}))
            guest.request('GET', '/g/' + original['id'] + '/meta', expected=(404,))
            require(client.json('POST', route, body, fields, expected=(status,)) == original,
                    'retry reminted a revoked capsule')
        large_stream(admin, client, name, backend, backup_spec, large_mib)
        print(f'PASS ({backend}): scoped enrollment, nested collections, reordered/repeated '
              f'parts, auto commits, checksum lookup, Files Range, restart receipts and idempotent/revoked grabs '
              f'({time.monotonic()-started:.1f}s)', flush=True)
    except SmokeFailure:
        # Inspect logs locally but emit only fixed categories, never log lines.
        logs = docker('logs', '--tail', '150', name, check=False)
        raw_logs = logs.stdout + logs.stderr
        if 'panic:' in raw_logs:
            print(f'DIAGNOSTIC ({backend}): server panic', flush=True)
        state = docker('inspect', '--format', '{{.State.Status}} {{.State.OOMKilled}} {{.State.ExitCode}}',
                       name, check=False).stdout.strip()
        if re.fullmatch(r'(running|exited|restarting|dead) (true|false) [0-9]+', state):
            print(f'DIAGNOSTIC ({backend}): container {state}', flush=True)
        lines = [line.partition('error=')[2] for line in raw_logs.splitlines()
                 if 'mobile finalize deferred:' in line and 'error=' in line]
        text = '\n'.join(lines)
        categories = {
            'invalid_media_or_spec': 'invalid photo upload specification',
            'checksum': 'checksum',
            'source_missing': 'file is not in the library',
            'restic_storage': 'restic',
            'locked_vault': 'vault is locked',
            'canceled_context': 'context canceled',
            'authorization': 'authentication',
            'catalog_conflict': 'catalog entry',
            'encrypted_stage': 'encrypted archive is incomplete or corrupt',
            'parts_conflict': 'mobile upload identity conflict',
            'parts_invalid': 'invalid mobile parts specification',
            'storage_corrupt': 'mobile upload staging corrupt',
            'storage_missing': 'no such file or directory',
        }
        matches = [code for code, marker in categories.items() if marker in text]
        if matches:
            print(f'DIAGNOSTIC ({backend}): ' + ', '.join(matches), flush=True)
        for line in lines:
            # Only classify syscall locations; never emit arbitrary log/error text.
            match = re.search(r'(open|stat|lstat|rename|mkdir|fork/exec) (/[^:]+): no such file or directory', line)
            if match:
                path = re.sub(r'[0-9a-f]{16,64}', '{id}', match.group(2))
                for private in [password, vault_phrase, getattr(locals().get('client'), 'token', '')]:
                    if private:
                        path = path.replace(private, '[secret]')
                if re.fullmatch(r'/[a-zA-Z0-9_./{}\-]+', path):
                    print(f'DIAGNOSTIC ({backend}): missing fixture path {path}', flush=True)
        raise
    finally:
        docker('rm', '--force', name, check=False)
        docker('volume', 'rm', volume, check=False)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--backend', choices=['both', 'restic', 'shared-experimental'], default='both')
    parser.add_argument('--extended-photos', action='store_true', help='also test Hidden opaque originals')
    parser.add_argument('--large-mib', type=int, default=250, help='generated large fixture size (default: 250 MiB)')
    args = parser.parse_args()
    if not 33 <= args.large_mib <= 1024:
        parser.error('--large-mib must be between 33 and 1024 for isolated smoke fixtures')
    image = os.environ.get('WEAZLCLOUD_IMAGE', 'weazlcloud:smoke')
    backend = 'setup'
    try:
        verify_local_docker()
        docker('image', 'inspect', image)
        backends = ['restic', 'shared-experimental'] if args.backend == 'both' else [args.backend]
        failed = False
        for backend in backends:
            try:
                run_backend(image, backend, args.extended_photos, args.large_mib)
            except SmokeFailure as error:
                print(f'FAIL ({backend}): {error}', file=sys.stderr)
                failed = True
            except Exception as error:
                print(f'FAIL ({backend}): unexpected {type(error).__name__}', file=sys.stderr)
                failed = True
        return int(failed)
    except SmokeFailure as error:
        print(f'FAIL ({backend}): {error}', file=sys.stderr)
        return 1
    except Exception as error:
        # Never dump HTTP JSON, credentials, headers, Docker logs or tracebacks.
        print(f'FAIL ({backend}): unexpected {type(error).__name__}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
