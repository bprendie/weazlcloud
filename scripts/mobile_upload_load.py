"""Bounded load and slow-body helpers for the disposable throughput harness."""
import hashlib
import http.client
import json
from pathlib import Path
import subprocess
import threading
import time
import urllib.parse

ROUTE = '/api/v1/photos/uploads'


class PollingLoad:
    def __init__(self, fixture, args):
        self.fixture, self.args = fixture, args
        self.ids, self.latencies, self.errors = [], [], 0
        self.lock, self.done = threading.Lock(), threading.Event()
        self.thread = threading.Thread(target=self.run, daemon=True)

    def add(self, identity):
        with self.lock:
            self.ids.append(identity)

    def start(self):
        self.thread.start()

    def stop(self):
        self.done.set()
        self.thread.join(timeout=35)
        if self.thread.is_alive():
            raise RuntimeError('status load did not drain')

    def run(self):
        client = self.fixture.client()
        while not self.done.is_set():
            with self.lock:
                ids = list(self.ids)
            start = time.monotonic()
            try:
                if ids:
                    if self.args.status_mode == 'batch':
                        client.json('POST', ROUTE + '/status', {'upload_ids': ids}, timeout=30)
                    else:
                        client.json('GET', ROUTE + '/' + ids[len(self.latencies) % len(ids)], timeout=30)
                    self.latencies.append(time.monotonic() - start)
            except Exception:
                self.errors += 1
            self.done.wait(5 if self.args.status_mode == 'batch' else 1 / 7)

    def metrics(self):
        ordered = sorted(self.latencies)
        return {'mode': self.args.status_mode, 'requests': len(ordered), 'errors': self.errors,
                'p50_ms': round(ordered[len(ordered) // 2] * 1000, 2) if ordered else None,
                'p95_ms': round(ordered[min(len(ordered) - 1, int(len(ordered) * .95))] * 1000, 2) if ordered else None}


def seed_catalog(fixture, count):
    root = Path(__file__).resolve().parents[1]
    helper = root / '.build/mobile-bench-catalog'
    if not helper.exists():
        raise RuntimeError('build .build/mobile-bench-catalog first')
    from subprocess import DEVNULL
    subprocess.run(['docker', 'exec', fixture.name, 'touch', '/data/.weazl-throughput-fixture'], check=True, stdout=DEVNULL)
    fixture.stop()
    subprocess.run(['docker', 'run', '--rm', '--network', 'none', '--user', '7272:7272',
                    '--volume', fixture.volume + ':/data', '--volume', str(helper) + ':/helper:ro',
                    '--entrypoint', '/helper', fixture.image, '/data', str(count)], check=True)
    fixture.start()
    fixture.admin.json('POST', '/api/login', {'username': 'throughput', 'password': fixture.password})
    fixture.admin.json('POST', '/api/unlock', {'passphrase': fixture.phrase})


def responsive(image, backend, args):
    # Import the already loaded harness instead of creating another fixture module.
    import sys
    h = sys.modules['__main__']
    with h.Fixture(image, backend, args) as f:
        body = h.png(901, 256, 256)
        spec = h.make_spec(f, 901, body, auto=False)
        transfer = h.smoke.transfer_create(f.client(), ROUTE, spec)
        ident = transfer['id']
        origin = urllib.parse.urlsplit(f.origin)
        conn = http.client.HTTPConnection(origin.hostname, origin.port, timeout=15)
        conn.putrequest('PUT', ROUTE + '/' + ident + '/components/original/parts/0')
        for key, value in {'Authorization': 'Bearer ' + f.token, 'X-Weazl-Desk': '1', 'Content-Length': str(len(body)),
                           'X-Weazl-SHA256': hashlib.sha256(body).hexdigest()}.items():
            conn.putheader(key, value)
        conn.endheaders()
        conn.send(body[:1024])
        # Give the server a chance to enter its body read, then exercise actual
        # HTTP status requests while the same upload cannot complete.
        time.sleep(.1)
        latencies = []
        try:
            for _ in range(5):
                before = time.monotonic()
                v = f.client().json('GET', ROUTE + '/' + ident, timeout=2)['transfer']
                h.smoke.require(v['components'][0]['received_bytes'] == 0, 'partial body acknowledged')
                latencies.append(time.monotonic() - before)
            caps = f.client().json('GET', '/api/v1/mobile/capabilities')
            h.smoke.require(caps['features']['upload_status_batch_v1'], 'batch capability missing')
            result = f.client().json('POST', ROUTE + '/status', {'upload_ids': [ident]})
            h.smoke.require(result['items'][0]['transfer']['status'] == 'uploading', 'batch state mismatch')
            conn.send(body[1024:])
            response = conn.getresponse()
            h.smoke.require(response.status == 200, 'slow-body transfer failed')
            response.read()
            f.client().json('POST', ROUTE + '/' + ident + '/finalize', {})
            stored = h.wait_stored(f.client(), ident, args.timeout)
            h.verify_original(f.client(), stored, body, spec['hidden'])
            h.smoke.require(max(latencies) < .5, 'stalled-body status exceeds 500 ms')
            # A disconnected body earns no receipt; the same upload can retry.
            retry_spec = h.make_spec(f, 902, body, auto=False)
            retry = h.smoke.transfer_create(f.client(), ROUTE, retry_spec)
            broken = http.client.HTTPConnection(origin.hostname, origin.port, timeout=5)
            broken.putrequest('PUT', ROUTE + '/' + retry['id'] + '/components/original/parts/0')
            for key, value in {'Authorization': 'Bearer ' + f.token, 'X-Weazl-Desk': '1', 'Content-Length': str(len(body)),
                               'X-Weazl-SHA256': hashlib.sha256(body).hexdigest()}.items():
                broken.putheader(key, value)
            broken.endheaders()
            broken.send(body[:1024])
            broken.close()
            time.sleep(.2)
            before_retry = f.client().json('GET', ROUTE + '/' + retry['id'])['transfer']
            h.smoke.require(before_retry['components'][0]['received_bytes'] == 0, 'disconnect acknowledged partial bytes')
            h.put_part(f.client(), retry['id'], body)
            f.client().json('POST', ROUTE + '/' + retry['id'] + '/finalize', {})
            replayed = h.wait_stored(f.client(), retry['id'], args.timeout)
            h.verify_original(f.client(), replayed, body, retry_spec['hidden'])
            print('RESPONSIVE ' + json.dumps({'backend': backend, 'status_max_ms': round(max(latencies) * 1000, 2),
                                             'slow_body_hash': 'verified', 'batch_status': 'verified', 'disconnect_resume': 'verified'}), flush=True)
        finally:
            conn.close()
