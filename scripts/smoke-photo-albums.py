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
import uuid
import zipfile
from pathlib import Path
from playwright.sync_api import sync_playwright, expect
from smoke_music_grid import smoke_music

name = 'weazl-albums-' + uuid.uuid4().hex[:10]
volume = name + '-data'
port = int(os.environ.get('WEAZLCLOUD_ALBUM_PORT', '19380'))
base = f'http://127.0.0.1:{port}'
image = os.environ.get('WEAZLCLOUD_IMAGE', 'weazlcloud:album-smoke')
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
        docker('run', '-d', '--name', name, '-p', f'127.0.0.1:{port}:7272',
               '-e', 'WEAZLCLOUD_DATA=/data', '-e', 'WEAZLCLOUD_DESK_ADDR=:7272',
               '-e', 'WEAZLCLOUD_SHARE_ADDR=:7273', '-e', 'WEAZLCLOUD_DRIVE_ADDR=:7274',
               '-e', 'WEAZLCLOUD_IMPORT_DIR=/import', '-e', 'WEAZLCLOUD_IMPORT_OWNER=albums',
               '-v', volume+':/data', '-v', root+':/import:ro', image)
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=shutil.which('chromium'), args=['--no-sandbox'])
            context = browser.new_context(base_url=base, viewport={'width': 1440, 'height': 1000})
            for _ in range(80):
                try:
                    if context.request.get('/ready').ok: break
                except Exception: pass
                time.sleep(.25)
            else: raise AssertionError('container not ready')
            def post(path, data):
                response = context.request.post(path, data=data, headers=headers)
                assert response.ok, response.text()
                return response.json()
            post('/api/bootstrap', {'username':'albums','password':'album-test-pass','vault_passphrase':'album-test-pass','confirm':'album-test-pass'})
            post('/api/unlock', {'passphrase':'album-test-pass'})
            page = context.new_page()
            errors = []
            page.on('pageerror', lambda error: errors.append(str(error)))
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
            assert page.locator('.photos-tabs [aria-pressed=true]').evaluate('(el) => getComputedStyle(el).color !== getComputedStyle(el).backgroundColor')
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
            page.locator('.dialog-close').click()
            page.locator('nav [data-view="takeout"]').click()
            import_zip('part2.zip')
            page.locator('nav [data-view="photos"]').click()
            page.locator('[data-photos-mode="albums"]').first.click()
            expect(page.locator('.photo-album-card')).to_have_count(2)
            page.locator('[data-photo-album="Google Takeout/Photos/Trip"]').click()
            expect(page.locator('.library-card')).to_have_count(2)
            page.screenshot(path='/tmp/weazl-photo-albums-smoke.png', full_page=True)
            docker('restart', name)
            for _ in range(80):
                try:
                    if context.request.get('/ready').ok: break
                except Exception: pass
                time.sleep(.25)
            post('/api/login', {'username':'albums','password':'album-test-pass'})
            post('/api/unlock', {'passphrase':'album-test-pass'})
            page.reload()
            expect(page.locator('.library-card')).to_have_count(2)
            assert not errors, errors
            smoke_music(context, page, post)
            assert not errors, errors
            assert (Path(root)/'part1.zip').exists()
            browser.close()
            print('PASS: split ZIP albums, metadata titles, duplicate titles stay separate, covers, preview, deep links, restart; ZIPs retained')
finally:
    subprocess.run(['docker','rm','-f',name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.run(['docker','volume','rm',volume], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
