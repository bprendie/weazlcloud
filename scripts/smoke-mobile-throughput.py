#!/usr/bin/env python3
"""Disposable local Docker parts-v1 throughput and upgrade smoke (stdlib only).

Example: python3 scripts/smoke-mobile-throughput.py --image NEW --old-image OLD
Both images must already exist locally. Default: 12 unique generated PNGs,
four asset upload lanes, 4 CPUs/4 GiB, both storage backends. The old-image run
uses a fresh volume for its baseline; upgrade fixtures use a third volume.
Three complete upgrade seeds are queued concurrently on the old image before
a graceful stop; at least one must still be queued/verifying before stopping.
The new image must finish these automatically. This does not simulate queued24h
expiry. A separate incomplete seed resumes exact parts. No performance thresholds.
Use the existing full mobile/browser smokes for broader Hidden/Live UI coverage.
--full-smokes also runs the imported full native smoke with its 250 MiB fixture.
Only fixed errors and aggregate metrics are printed; never credentials/bodies.
Stored timestamps are client observations with 0.5-second bounded polling;
preview completion is observed after stored, at one-second intervals. Results
include that observation lag. Original readback is outside the timed interval.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import hashlib
import importlib.util
import json
import math
import os
from pathlib import Path
import secrets
import struct
import subprocess
import sys
import time
import uuid
import zlib


sys.dont_write_bytecode = True
SOURCE = Path(__file__).with_name('smoke-mobile-server.py')
SPEC = importlib.util.spec_from_file_location('mobile_smoke_fixtures', SOURCE)
smoke = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(smoke)
ROUTE = '/api/v1/photos/uploads'


def png(index, width=768, height=768):
    """Valid deterministic RGB noise: unique pixels, no padded fake media/dedupe."""
    pixels = hashlib.shake_256(('mobile-throughput-v1-' + str(index)).encode()).digest(width * height * 3)
    raw = b''.join(b'\0' + pixels[y * width * 3:(y + 1) * width * 3] for y in range(height))

    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))

    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, 2, 0, 0, 0))
            + chunk(b'IDAT', zlib.compress(raw, 0)) + chunk(b'IEND', b''))


class Fixture:
    def __init__(self, image, backend, args):
        self.image, self.backend, self.args = image, backend, args
        self.name = 'weazl-throughput-' + uuid.uuid4().hex[:12]
        self.volume = self.name + '-data'
        self.password, self.phrase = secrets.token_urlsafe(24), secrets.token_urlsafe(24)

    def start(self):
        smoke.docker('run', '--detach', '--pull', 'never', '--name', self.name,
                     '--publish', '127.0.0.1::7272', '--cpus', str(self.args.cpus),
                     '--memory', self.args.memory, '--memory-swap', self.args.memory,
                     '--stop-timeout', '60', '--env', 'WEAZLCLOUD_DATA=/data',
                     '--env', 'WEAZLCLOUD_DESK_ADDR=:7272',
                     '--env', 'WEAZLCLOUD_SHARE_ADDR=:7273',
                     '--env', 'WEAZLCLOUD_DRIVE_ADDR=:7274',
                     '--env', 'WEAZLCLOUD_STORAGE_BACKEND=' + self.backend,
                     '--volume', self.volume + ':/data', self.image)
        self.origin = smoke.fixture_origin(self.name, 7272)
        self.admin = smoke.Client(self.origin, cookies=True)
        self.admin.wait_ready()

    def __enter__(self):
        try:
            smoke.docker('volume', 'create', self.volume)
            self.start()
            self.admin.json('POST', '/api/bootstrap', {'username': 'throughput', 'password': self.password,
                            'vault_passphrase': self.phrase, 'confirm': self.phrase}, expected=(201,))
            self.admin.json('POST', '/api/unlock', {'passphrase': self.phrase})
            enrolled = self.admin.json('POST', '/api/v1/devices',
                                       {'name': 'Throughput fixture', 'scopes': smoke.SCOPES}, expected=(201,))
            self.token, self.device = enrolled['token'], enrolled['device']['id']
            self.album = smoke.collections(self.client())['id']
            self.admin.json('POST', '/api/photos/preparation', {'action': 'resume'})
            return self
        except BaseException:
            self.close()
            raise

    def client(self):
        return smoke.Client(self.origin, self.token)

    def stop(self):
        smoke.docker('stop', '--time', '60', self.name, timeout=75)
        state = smoke.docker('inspect', '--format', '{{.State.ExitCode}} {{.State.OOMKilled}}', self.name).stdout.strip()
        smoke.require(state == '0 false', 'fixture did not drain cleanly within 60 seconds')
        smoke.docker('rm', self.name)

    def replace(self, image):
        self.stop()
        self.image = image
        self.start()
        self.admin.json('POST', '/api/login', {'username': 'throughput', 'password': self.password})
        self.admin.json('POST', '/api/unlock', {'passphrase': self.phrase})

    def close(self):
        # Cleanup only our random container/volume. Stop has a 60-second grace;
        # Docker removal follows the stop, never a five-second forced restart.
        smoke.docker('stop', '--time', '60', self.name, check=False, timeout=75)
        smoke.docker('rm', self.name, check=False)
        removed = smoke.docker('volume', 'rm', self.volume, check=False)
        smoke.require(removed.returncode == 0, 'disposable fixture cleanup failed')

    def __exit__(self, *unused):
        self.close()


def make_spec(fixture, index, body, auto=True):
    spec = smoke.photo_spec(fixture.device, fixture.album, body, hidden=index % 4 == 0)
    spec.update(device_asset_id='throughput-' + str(index), commit_when_complete=auto)
    return spec


def put_part(client, upload_id, body, index=0, component='original'):
    part = body[index * smoke.PART_SIZE:(index + 1) * smoke.PART_SIZE]
    smoke.require(bool(part), 'empty fixture part')
    return client.json('PUT', ROUTE + '/' + upload_id + '/components/' + component + '/parts/' + str(index),
                      part, {'Content-Type': 'application/octet-stream',
                             'X-Weazl-SHA256': hashlib.sha256(part).hexdigest()})['transfer']


def wait_stored(client, upload_id, timeout):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        view = client.json('GET', ROUTE + '/' + upload_id, timeout=min(30, timeout))['transfer']
        smoke.require(view['status'] not in ('failed', 'cancelled'), 'background finalization failed')
        if view['status'] == 'stored':
            smoke.require(view['id'] == upload_id and view['result']['status'] == 'stored', 'stored receipt mismatch')
            return view
        time.sleep(.5)
    raise smoke.SmokeFailure('stored receipt deadline exceeded')


def verify_original(client, view, body, hidden):
    digest = hashlib.sha256()
    client.request('GET', '/api/v1/photos/assets/' + view['result']['asset_id'] + '/original'
                   + ('?hidden=1' if hidden else ''), sink=digest.update, timeout=120)
    smoke.require(digest.digest() == hashlib.sha256(body).digest(), 'original hash changed')


def verify_live(fixture, record):
    if 'motion' not in record:
        return
    client = fixture.client()
    detail = client.json('GET', '/api/v1/photos/assets/' + record['view']['result']['asset_id'])
    components = {c['id']: c['asset_id'] for c in detail['components']}
    smoke.require(set(components) == {'original', 'motion'}, 'Live Photo component relationship changed')
    digest = hashlib.sha256()
    client.request('GET', '/api/v1/photos/assets/' + components['motion'] + '/original', sink=digest.update)
    smoke.require(digest.digest() == hashlib.sha256(record['motion']).digest(), 'Live Photo motion hash changed')


def verify_membership(fixture, records):
    client = fixture.client()
    normal = client.json('GET', '/api/v1/photos?limit=100')['items']
    hidden = client.json('GET', '/api/v1/photos?limit=100&mode=hidden')['items']
    normal_ids, hidden_ids = {v['id'] for v in normal}, {v['id'] for v in hidden}
    for record in records:
        asset = record['view']['result']['asset_id']
        expected, excluded = (hidden_ids, normal_ids) if record['spec']['hidden'] else (normal_ids, hidden_ids)
        smoke.require(asset in expected and asset not in excluded, 'Hidden visibility changed')
    # Selection uses the existing full smoke's album contract, including Hidden scope.
    for visibility in (False, True):
        expected = sum(r['spec']['hidden'] == visibility for r in records)
        if not expected:
            continue
        selection = client.json('POST', '/api/v1/photos/selections',
                                {'mode': 'hidden' if visibility else 'all', 'filter': {'album': 'album:' + fixture.album,
                                                         'hidden': visibility}}, expected=(201,))
        smoke.require(selection['count'] == expected, 'album membership changed')


def percentile(values, fraction):
    return sorted(values)[max(0, math.ceil(len(values) * fraction) - 1)]


def benchmark(image, backend, args, payloads, label):
    with Fixture(image, backend, args) as fixture:
        started = time.monotonic()
        records = []

        def upload(index):
            client, body = fixture.client(), payloads[index]
            spec = make_spec(fixture, index, body)
            before = time.monotonic()
            transfer = smoke.transfer_create(client, ROUTE, spec)
            latency = time.monotonic() - before
            for part in range((len(body) + smoke.PART_SIZE - 1) // smoke.PART_SIZE):
                accepted = put_part(client, transfer['id'], body, part)
            smoke.require(accepted['components'][0]['received_bytes'] == len(body), 'landed counters mismatch')
            return {'id': transfer['id'], 'spec': spec, 'body': body,
                    'create_s': latency, 'landed_s': time.monotonic() - started}

        # Separate pools: waiting for stored must never occupy an upload lane.
        with ThreadPoolExecutor(max_workers=args.concurrency) as uploads, ThreadPoolExecutor(max_workers=args.concurrency) as polls:
            waiting = {}
            for future in as_completed([uploads.submit(upload, i) for i in range(len(payloads))]):
                record = future.result()
                records.append(record)

                def observe(record=record):
                    view = wait_stored(fixture.client(), record['id'], args.timeout)
                    return view, time.monotonic() - started

                waiting[polls.submit(observe)] = record
            for future in as_completed(waiting):
                record = waiting[future]
                record['view'], record['stored_s'] = future.result()

        stored_s = max(r['stored_s'] for r in records)
        landed_s = max(r['landed_s'] for r in records)
        # Observe background preparation only: no thumbnail request to render on demand.
        preview_deadline = time.monotonic() + args.timeout
        while time.monotonic() < preview_deadline:
            prep = fixture.admin.json('GET', '/api/photos/preparation')
            if prep.get('status') == 'complete' and prep.get('total', 0) >= len(records) and prep.get('ready', 0) >= len(records):
                break
            time.sleep(1)
        else:
            raise smoke.SmokeFailure('asynchronous preview deadline exceeded')
        preview_s = time.monotonic() - started
        for record in records:
            verify_original(fixture.client(), record['view'], record['body'], record['spec']['hidden'])
            replay = smoke.transfer_create(fixture.client(), ROUTE, record['spec'])
            smoke.require(replay['id'] == record['id'] and replay['result'] == record['view']['result'], 'receipt replay changed')
        verify_membership(fixture, records)
        total = sum(map(len, payloads)) / (1 << 20)
        metrics = {'run': label, 'backend': backend, 'cpus': args.cpus, 'memory': args.memory,
                   'assets': len(records), 'concurrency': args.concurrency, 'mib': round(total, 3),
                   'all_landed_s': round(landed_s, 3), 'all_stored_observed_s': round(stored_s, 3),
                   'landed_mib_s': round(total / landed_s, 3), 'stored_mib_s': round(total / stored_s, 3),
                   'create_p50_ms': round(percentile([r['create_s'] for r in records], .5) * 1000, 2),
                   'create_p95_ms': round(percentile([r['create_s'] for r in records], .95) * 1000, 2),
                   'landed_to_stored_p50_s': round(percentile([r['stored_s'] - r['landed_s'] for r in records], .5), 3),
                   'landed_to_stored_p95_s': round(percentile([r['stored_s'] - r['landed_s'] for r in records], .95), 3),
                   'preview_complete_observed_s': round(preview_s, 3),
                   'preview_after_stored_s': round(preview_s - stored_s, 3)}
        print('METRICS ' + json.dumps(metrics, sort_keys=True), flush=True)
        return metrics


def upgrade(old, new, backend, args):
    with Fixture(old, backend, args) as fixture:
        records = []
        # A real >16 MiB PNG ensures the partial seed contains an independently
        # acknowledged tail receipt; no hand edits to encrypted staging metadata.
        generated = subprocess.run(['docker', 'exec', fixture.name, 'ffmpeg', '-v', 'error',
                                    '-f', 'lavfi', '-i', 'color=blue:s=32x24:r=10:d=1',
                                    '-threads', '1', '-c:v', 'libx264', '-movflags',
                                    'frag_keyframe+empty_moov', '-f', 'mov', 'pipe:1'],
                                   capture_output=True, timeout=30)
        smoke.require(generated.returncode == 0 and bool(generated.stdout), 'generated motion fixture failed')
        for index, body in ((100, png(100)), (101, png(101, 2560, 2304)),
                            (102, png(102)), (103, png(103))):
            spec = make_spec(fixture, index, body, auto=False)
            if index == 102:
                spec['components'].append({'id': 'motion', 'filename': 'fixture.mov',
                                           'size': len(generated.stdout),
                                           'sha256': hashlib.sha256(generated.stdout).hexdigest()})
            transfer = smoke.transfer_create(fixture.client(), ROUTE, spec)
            tail = (len(body) - 1) // smoke.PART_SIZE
            view = put_part(fixture.client(), transfer['id'], body, tail)
            if index == 102:
                view = put_part(fixture.client(), transfer['id'], generated.stdout, component='motion')
            records.append({'id': transfer['id'], 'spec': spec, 'body': body, 'tail': tail, 'before': view})
            if index == 102:
                records[-1]['motion'] = generated.stdout
        smoke.require(records[0]['before']['status'] == 'uploading'
                      and records[1]['tail'] == 1, 'upgrade seed contract mismatch')
        partial = records[1]
        fixture.client().request('PUT', ROUTE + '/' + partial['id'] + '/components/original/parts/0',
                                 partial['body'][:smoke.PART_SIZE],
                                 {'X-Weazl-SHA256': '0' * 64}, expected=(422,))
        failed = fixture.client().json('GET', ROUTE + '/' + partial['id'])['transfer']
        smoke.require(failed['components'] == partial['before']['components'], 'rejected part created accepted progress')

        complete = [record for record in records if record is not partial]
        with ThreadPoolExecutor(max_workers=len(complete)) as pool:
            def enqueue(record):
                return fixture.client().json('POST', ROUTE + '/' + record['id'] + '/finalize', {})['transfer']
            accepted = list(pool.map(enqueue, complete))
        smoke.require(all(view['status'] in ('queued', 'verifying') for view in accepted),
                      'old image did not acknowledge queued finalization')
        for record in records:
            record['pre_stop'] = fixture.client().json('GET', ROUTE + '/' + record['id'])['transfer']
        queued = sum(record['pre_stop']['status'] in ('queued', 'verifying') for record in complete)
        smoke.require(queued > 0, 'upgrade seed has no queued/verifying work before stop')
        smoke.require(partial['pre_stop']['status'] == 'uploading', 'incomplete seed unexpectedly finalized')
        print('UPGRADE pre-stop ' + json.dumps({'backend': backend, 'queued_or_verifying': queued,
                                               'complete': len(complete), 'partial': 1}), flush=True)
        fixture.replace(new)
        recovered_states = []
        for record in records:
            client = fixture.client()
            # Read only: complete jobs must resume without create/retry/finalize
            # requests that could conceal a missing durable queue marker.
            replay = client.json('GET', ROUTE + '/' + record['id'])['transfer']
            recovered_states.append(replay['status'])
            smoke.require(replay['id'] == record['id'] and replay['components'] == record['before']['components'],
                          'upgrade changed ID or accepted part receipts')
            if record is partial:
                smoke.require(replay['status'] == 'uploading', 'upgrade did not preserve incomplete staging')
        print('UPGRADE post-unlock ' + json.dumps({'backend': backend,
              'states': {state: recovered_states.count(state) for state in sorted(set(recovered_states))}}), flush=True)
        for record in complete:
            record['view'] = wait_stored(fixture.client(), record['id'], args.timeout)
        # Verify the incomplete receipt remains unchanged even after peers commit.
        client = fixture.client()
        replay = client.json('GET', ROUTE + '/' + partial['id'])['transfer']
        smoke.require(replay['status'] == 'uploading' and replay['components'] == partial['before']['components'],
                      'peer finalization changed incomplete staging')
        missing = client.json('GET', ROUTE + '/' + partial['id'] + '/parts?component=original')
        smoke.require(missing['missing'] == [0] and not missing['has_more'], 'upgrade missing-parts mismatch')
        duplicate = put_part(client, partial['id'], partial['body'], partial['tail'])
        smoke.require(duplicate['components'] == replay['components'], 'duplicate receipt advanced counters')
        for part in missing['missing']:
            put_part(client, partial['id'], partial['body'], part)
        client.json('POST', ROUTE + '/' + partial['id'] + '/finalize', {})
        partial['view'] = wait_stored(client, partial['id'], args.timeout)
        for record in records:
            verify_original(fixture.client(), record['view'], record['body'], record['spec']['hidden'])
            verify_live(fixture, record)
        fixture.replace(new)
        for record in records:
            replay = smoke.transfer_create(fixture.client(), ROUTE, record['spec'])
            smoke.require(replay['id'] == record['id'] and replay['result'] == record['view']['result'], 'stored upgrade replay changed')
            verify_original(fixture.client(), replay, record['body'], record['spec']['hidden'])
            verify_live(fixture, record)
        verify_membership(fixture, records)
        print('PASS upgrade (' + backend + '): queued work resumed automatically; partial receipts, IDs, hashes, albums, Hidden, Live Photo components, stored restart replay', flush=True)


def full_mobile(image, backend, args):
    """Reuse full fixture coverage, extending its old five-second restart grace."""
    original = smoke.docker
    settings = {'WEAZLCLOUD_SMOKE_CPUS': str(args.cpus), 'WEAZLCLOUD_SMOKE_MEMORY': args.memory}
    previous = {key: os.environ.get(key) for key in settings}

    def graceful(*command, **kwargs):
        if command[0] == 'restart':
            command = ('restart', '--time', '60', command[-1])
            kwargs['timeout'] = 75
        elif command[:2] == ('rm', '--force'):
            original('stop', '--time', '60', command[-1], check=False, timeout=75)
            command = ('rm', command[-1])
        return original(*command, **kwargs)

    try:
        os.environ.update(settings)
        smoke.docker = graceful
        smoke.run_backend(image, backend, True, 250)
    finally:
        smoke.docker = original
        for key, value in previous.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', default=os.environ.get('WEAZLCLOUD_IMAGE', 'weazlcloud:smoke'))
    parser.add_argument('--old-image', help='optional local baseline image and upgrade source')
    parser.add_argument('--backend', choices=('both', 'restic', 'shared-experimental'), default='both')
    parser.add_argument('--count', type=int, choices=range(12, 17), default=12)
    parser.add_argument('--concurrency', type=int, choices=range(1, 9), default=4)
    parser.add_argument('--cpus', type=int, choices=(4, 8), default=4)
    parser.add_argument('--memory', choices=('4g', '8g'), default='4g')
    parser.add_argument('--timeout', type=int, default=300)
    parser.add_argument('--full-smokes', action='store_true', help='also run existing extended native smoke and 250 MiB fixture')
    args = parser.parse_args()
    if not 30 <= args.timeout <= 1800:
        parser.error('--timeout must be 30..1800 seconds')
    try:
        smoke.verify_local_docker()
        # Pin mutable tags once: concurrent builders cannot change a comparison.
        def pin(image):
            return smoke.docker('image', 'inspect', '--format', '{{.Id}}', image).stdout.strip()
        new, old = pin(args.image), pin(args.old_image) if args.old_image else None
        print('IMAGES ' + json.dumps({'new': new, 'baseline': old}), flush=True)
        payloads = [png(i) for i in range(args.count)]
        print('DATASET ' + hashlib.sha256(b''.join(hashlib.sha256(p).digest() for p in payloads)).hexdigest(), flush=True)
        backends = ('restic', 'shared-experimental') if args.backend == 'both' else (args.backend,)
        for backend in backends:
            baseline = benchmark(old, backend, args, payloads, 'baseline') if old else None
            candidate = benchmark(new, backend, args, payloads, 'new')
            if baseline:
                print('COMPARISON ' + json.dumps({'backend': backend, 'stored_rate_ratio': round(candidate['stored_mib_s'] / baseline['stored_mib_s'], 3),
                                                 'note': 'single cold disposable run each; polling observations, no performance assertion'}), flush=True)
                upgrade(old, new, backend, args)
            if args.full_smokes:
                full_mobile(new, backend, args)
        return 0
    except smoke.SmokeFailure as error:
        print('FAIL: ' + str(error), file=sys.stderr)
    except Exception as error:
        print('FAIL: unexpected ' + type(error).__name__, file=sys.stderr)
    return 1


if __name__ == '__main__':
    sys.exit(main())
