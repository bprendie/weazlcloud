#!/usr/bin/env python3
"""Disposable Docker/Chromium album smoke. Requires Python playwright + Chromium.
WEAZLCLOUD_IMAGE=weazlcloud:album-smoke python scripts/smoke-photo-albums.py
"""
import base64
import os
import shutil
import subprocess
import tempfile
import time
from urllib.parse import urlparse
import uuid
import zipfile
from pathlib import Path
from playwright.sync_api import sync_playwright, expect
from smoke_music_grid import smoke_music
from smoke_library_ui import smoke_library_ui
from smoke_photo_preparation import smoke_photo_preparation
from smoke_modal_photos import smoke_modal_photos

name = 'weazl-albums-' + uuid.uuid4().hex[:10]
volume = name + '-data'
port = int(os.environ.get('WEAZLCLOUD_ALBUM_PORT', '19380'))
base = f'http://127.0.0.1:{port}'
image = os.environ.get('WEAZLCLOUD_IMAGE', 'weazlcloud:album-smoke')
storage_backend = os.environ.get('WEAZLCLOUD_SMOKE_STORAGE_BACKEND', 'restic')
host_network = os.environ.get('WEAZLCLOUD_SMOKE_HOST_NETWORK') == '1'
network_args = ['--network', 'host'] if host_network else ['-p', f'127.0.0.1:{port}:7272', '-p', f'127.0.0.1:{port+1}:7273']
resource_args = []
for setting, flag in [('WEAZLCLOUD_SMOKE_CPUS', '--cpus'), ('WEAZLCLOUD_SMOKE_MEMORY', '--memory')]:
    if os.environ.get(setting):
        resource_args += [flag, os.environ[setting]]
desk_addr = f'127.0.0.1:{port}' if host_network else ':7272'
share_addr = f'127.0.0.1:{port+1}' if host_network else ':7273'
drive_addr = f'127.0.0.1:{port+2}' if host_network else ':7274'
headers = {'X-Weazl-Desk': '1'}
png = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=')

def docker(*args):
    return subprocess.run(['docker', *args], check=True, capture_output=True, text=True)

try:
    with tempfile.TemporaryDirectory(prefix='weazl-albums-') as root:
        os.chmod(root, 0o755)
        for filename, entries in {
            'part1.zip': {
                'Takeout/Google Photos/Photos from 2020/a.png': png,
                'Takeout/Google Photos/Trip/a.png': png,
                'Takeout/Google Photos/Trip/metadata.json': '{"albumData":{"title":"Holiday <2020>","description":"At the lake"}}',
            },
            'part2.zip': {
                'Takeout/Google Photos/Trip/b.png': png,
                'Takeout/Google Photos/Another trip/c.png': png,
                'Takeout/Google Photos/Another trip/metadata.json': '{"title":"Holiday <2020>"}',
            },
        }.items():
            with zipfile.ZipFile(Path(root)/filename, 'w') as z:
                for path, content in entries.items():
                    z.writestr(path, content)
            os.chmod(Path(root)/filename, 0o644)
        docker('run', '-d', '--name', name, *network_args, *resource_args,
               '-e', 'WEAZLCLOUD_DATA=/data', '-e', f'WEAZLCLOUD_DESK_ADDR={desk_addr}',
               '-e', f'WEAZLCLOUD_SHARE_ADDR={share_addr}', '-e', f'WEAZLCLOUD_DRIVE_ADDR={drive_addr}',
               '-e', f'WEAZLCLOUD_STORAGE_BACKEND={storage_backend}',
               '-e', 'WEAZLCLOUD_IMPORT_DIR=/import', '-e', 'WEAZLCLOUD_IMPORT_OWNER=albums',
               '-v', volume+':/data', '-v', root+':/import:ro', image)
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=shutil.which('chromium'), args=['--no-sandbox'])
            context = browser.new_context(base_url=base, viewport={'width': 1440, 'height': 1000})
            for _ in range(80):
                try:
                    if context.request.get('/ready', timeout=2000).ok: break
                except Exception: pass
                time.sleep(.25)
            else: raise AssertionError('container not ready')
            if os.environ.get('WEAZLCLOUD_SMOKE_CPUS') == '2' and os.environ.get('WEAZLCLOUD_SMOKE_MEMORY') == '4g':
                logs = docker('logs', name)
                assert 'cpu ceiling=1 workers=1 background=1 readers=1 memory=536870912' in logs.stdout + logs.stderr, logs
            def post(path, data):
                response = context.request.post(path, data=data, headers=headers)
                assert response.ok, response.text()
                return response.json()
            post('/api/bootstrap', {'username':'albums','password':'album-test-pass','vault_passphrase':'album-test-pass','confirm':'album-test-pass'})
            post('/api/unlock', {'passphrase':'album-test-pass'})
            page = context.new_page()
            errors = []
            requests = []
            page.on('pageerror', lambda error: errors.append(str(error)))
            page.on('request', lambda request: requests.append(request.url))
            page.goto(base+'/#takeout')
            # Clicking navigation also exercises the normal page loading path.
            page.locator('nav [data-view="takeout"]').click()
            def import_zip(filename):
                page.locator(f'[data-import-zip="{filename}"]').click()
                for _ in range(160):
                    jobs = context.request.get('/api/takeout').json()['jobs']
                    job = next((j for j in jobs if j['name']==filename), None)
                    if job and job['status']=='failed': raise AssertionError(job)
                    if job and job['status']=='complete': return
                    time.sleep(.25)
                raise AssertionError('import timed out')
            import_zip('part1.zip')
            page.locator('nav [data-view="photos"]').click()
            page.locator('[data-photos-mode="albums"]').click()
            expect(page.locator('.photo-album-card')).to_have_count(1)
            # Album polling can replace the tab between locator resolution and
            # evaluation. Inspect the current connected element in one turn.
            page.wait_for_function('''() => {
                const el = document.querySelector('.photos-tabs [aria-pressed=true]');
                if (!el) return false;
                const style = getComputedStyle(el);
                return !!style.color && style.color !== style.backgroundColor;
            }''')
            expect(page.locator('.photo-album-card strong')).to_have_text('Holiday <2020>')
            for _ in range(80):
                if page.locator('.photo-album-cover img').evaluate('(img) => img.naturalWidth > 0'): break
                page.wait_for_timeout(250)
            else: raise AssertionError('album cover did not load')
            page.locator('.photo-album-card').click()
            expect(page.locator('#content h1')).to_have_text('Holiday <2020>')
            expect(page.locator('.library-card')).to_have_count(1)
            page.reload()
            expect(page.locator('.library-card')).to_have_count(1)
            page.locator('[data-select-file]').first.click()
            expect(page.locator('#modal')).to_be_visible()
            page.locator('[data-action="photo-viewer-close"]').click()
            page.locator('nav [data-view="takeout"]').click()
            import_zip('part2.zip')
            requests.clear()
            photo_started = time.perf_counter()
            page.goto(base+'/#photos')
            expect(page.locator('.photo-grid .library-card')).to_have_count(4)
            date_summary = context.request.get('/api/photos/dates', headers=headers).json()
            assert date_summary.get('unknown_dates') == 4, date_summary
            page.locator('[data-photos-mode="recent"]').click()
            expect(page.locator('.photo-grid .library-card')).to_have_count(4)
            assert page.url.endswith('#photos/recent'), page.url
            page.locator('[data-photos-mode="all"]').click()
            expect(page.locator('.photo-grid .library-card')).to_have_count(4)
            first_screen_ms = (time.perf_counter() - photo_started) * 1000
            photo_dom_cards = page.locator('.photo-grid .library-card').count()
            photo_requests = list(requests)
            assert not any(urlparse(url).path == '/api/library' for url in photo_requests), photo_requests
            assert page.locator('.photo-grid .library-card').count() <= 40
            photo_thumb = page.locator('.photo-grid [data-photo-thumbnail]').first
            photo_thumb.wait_for(state='attached')
            page.wait_for_function('''() => {
                const img = document.querySelector('.photo-grid [data-photo-thumbnail]');
                return img && img.complete && img.naturalWidth > 0;
            }''')
            first_thumbnail_ms = (time.perf_counter() - photo_started) * 1000
            page.locator('.photo-grid [data-select-file]').first.click()
            expect(page.locator('#modal.photo-viewer')).to_be_visible()
            expect(page.locator('.photo-viewer-top small')).to_have_text('1 of 4')
            page.locator('[data-action="photo-viewer-next"]').click()
            expect(page.locator('.photo-viewer-top small')).to_have_text('2 of 4')
            page.keyboard.press('ArrowLeft')
            expect(page.locator('.photo-viewer-top small')).to_have_text('1 of 4')
            page.locator('[data-action="photo-viewer-info"]').click()
            expect(page.locator('.photo-viewer-info')).to_be_visible()
            page.locator('[data-action="photo-viewer-close"]').click()
            expect(page.locator('#modal')).not_to_be_visible()
            if os.environ.get('WEAZLCLOUD_SMOKE_M3_ONLY') == '1':
                assert not errors, errors
                print('PASS: M3 Photos date summary, recent mode, full-screen viewer, previous/next, keyboard navigation and metadata drawer')
                browser.close()
                raise SystemExit(0)
            page.locator('.photo-tools > summary').click()
            page.locator('[data-action="photo-preparation"]').click()
            for _ in range(120):
                preparation = context.request.get('/api/photos/preparation', headers=headers).json()
                if preparation.get('status') == 'complete': break
                time.sleep(.25)
            else: raise AssertionError(f'photo preparation did not finish: {preparation}')
            assert preparation.get('ready') == 4 and preparation.get('bundle_ready') == 4 and preparation.get('failed') == 0, preparation
            page.locator('[data-photos-mode="albums"]').first.click()
            expect(page.locator('.photo-album-card')).to_have_count(2)
            page.locator('[data-photo-album="Photos/Trip"]').click()
            expect(page.locator('.library-card')).to_have_count(2)
            page.screenshot(path='/tmp/weazl-photo-albums-smoke.png', full_page=True)
            post('/api/photos/preparation', {'action': 'pause'})
            docker('restart', name)
            for _ in range(80):
                try:
                    if context.request.get('/ready', timeout=2000).ok: break
                except Exception: pass
                time.sleep(.25)
            post('/api/login', {'username':'albums','password':'album-test-pass'})
            post('/api/unlock', {'passphrase':'album-test-pass'})
            page.reload()
            expect(page.locator('.library-card')).to_have_count(2)
            assert not errors, errors
            smoke_photo_preparation(context, page, post, png)
            smoke_library_ui(page, storage_backend)
            assert not errors, errors
            smoke_modal_photos(context, page, post, browser, base, storage_backend)
            assert not errors, errors
            smoke_music(context, page, post)
            assert not errors, errors
            assert (Path(root)/'part1.zip').exists()
            browser.close()
            print(f'PHOTOS SMOKE: first 4 cards {first_screen_ms:.0f} ms; first thumbnail {first_thumbnail_ms:.0f} ms; DOM cards {photo_dom_cards}')
            print(f'PASS ({storage_backend}): paginated Photos without full-library listing, thumbnail IDs, preview preparation, split albums, metadata titles, covers, deep links and restart; ZIPs retained')
finally:
    subprocess.run(['docker','rm','-f',name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.run(['docker','volume','rm',volume], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
