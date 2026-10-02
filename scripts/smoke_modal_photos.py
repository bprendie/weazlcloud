"""Authenticated modal Photos checks on the existing five-photo fixture."""
import io
import json
import statistics
import time
import zipfile
from pathlib import Path
from playwright.sync_api import expect


def smoke_modal_photos(context, page, post, browser, base, backend):
    page.goto(base + '/#photos')
    expect(page.locator('.photo-grid .library-card')).to_have_count(5)
    items = context.request.get('/api/v1/photos?limit=10').json()['items']
    first, second = items[:2]
    post('/api/v1/photos/assets/' + first['id'], {'captured_at': '2011-05-06T12:00:00Z', 'caption': 'Sovereign <lake>', 'offset_known': True})
    post('/api/v1/photos/assets/' + second['id'], {'captured_at': '2020-06-07T12:00:00Z', 'offset_known': True})
    page.reload()
    expect(page.locator('[data-photo-date]').first.locator('option[value="2011"]')).to_have_count(1)
    before = time.perf_counter()
    page.locator('[data-photo-date]').first.select_option('2011')
    expect(page.locator('.photo-grid .library-card')).to_have_count(1)
    jump_ms = (time.perf_counter() - before) * 1000
    assert jump_ms < 1000, jump_ms
    page.locator('.photo-grid [data-select-file]').first.click()
    expect(page.locator('#modal.photo-viewer')).to_be_visible()
    page.locator('[data-action="photo-toggle-favorite"]').click()
    expect(page.locator('[data-action="photo-toggle-favorite"]')).to_have_attribute('aria-pressed', 'true')
    page.locator('[data-action="photo-viewer-close"]').click()
    page.locator('[data-photos-mode="favorites"]').click()
    expect(page.locator('.photo-grid .library-card')).to_have_count(1)
    page.locator('[data-photos-mode="all"]').click()
    expect(page.locator('.photo-grid .library-card')).to_have_count(5)
    page.locator('[data-photo-search]').fill('Sovereign*')
    expect(page.locator('.photo-grid .library-card')).to_have_count(1)
    page.locator('[data-photo-search]').fill('')
    expect(page.locator('.photo-grid .library-card')).to_have_count(5)
    page.locator('[data-photo-select-day="2011-05-06"]').click()
    page.locator('[data-action="photos-add-selected-to-album"]').click()
    page.locator('#photo-album-form [name="title"]').fill('Private <album>')
    with page.expect_response(lambda r: '/api/v1/photos/albums' in r.url and r.request.method == 'POST') as saved:
        page.locator('#photo-album-form button.primary').click()
    assert saved.value.ok, saved.value.text()
    card = page.locator('.photo-album-card').filter(has_text='Private <album>')
    expect(card).to_be_visible()
    assert card.locator('album').count() == 0, 'album title interpreted as HTML'
    card.locator('[data-action="edit-photo-album"]').click()
    page.locator('#photo-album-form [name="description"]').fill('Owner edited description')
    page.locator('#photo-album-form button.primary').click()
    expect(card).to_contain_text('Owner edited description')
    post('/api/node', {'hostname': 'grab.test'})
    card.locator('[data-action="share-photo-album"]').click()
    page.locator('#photo-grab-form [name="grabs"]').fill('4')
    page.locator('#photo-grab-form button.primary').click()
    expect(page.locator('#modal h2')).to_have_text('Gallery ready', timeout=30000)
    link = page.locator('#modal a').get_attribute('href')
    expect(page.locator('.mint-qr')).to_be_visible()
    page.locator('#modal [data-close]').click()
    smoke_guest_gallery(browser, base, link.rsplit('/', 1)[-1], backend)
    post('/api/v1/photos/folders', {'path': 'Photos/Trip', 'hidden': True})
    page.goto(base + '/#photos')
    expect(page.locator('.photo-grid .library-card')).to_have_count(3)
    page.locator('[data-photos-mode="hidden"]').click()
    expect(page.locator('.photo-grid .library-card')).to_have_count(2)
    post('/api/v1/photos/folders', {'path': 'Photos/Trip', 'hidden': False})
    page.locator('[data-photos-mode="all"]').click()
    expect(page.locator('.photo-grid .library-card')).to_have_count(5)
    smoke_photo_selection(context, page, base)
    smoke_photo_performance(context, page, base, backend, jump_ms)
    from smoke_photo_cache import smoke_photo_cache
    smoke_photo_cache(context,page,base,backend)
    from smoke_photo_timeline import smoke_photo_timeline
    smoke_photo_timeline(context,page,post,base)
    print('PASS: date jump, wildcard caption search, favorite, server day selection, album edit, gallery mint/QR and Hidden browser transitions')


def smoke_guest_gallery(browser, base, grant_id, backend):
    from urllib.parse import urlparse
    parsed = urlparse(base)
    guest_base = f'http://{parsed.hostname}:{parsed.port + 1}'
    guest = browser.new_context(base_url=guest_base, viewport={'width': 390, 'height': 844}, accept_downloads=True)
    try:
        page = guest.new_page()
        errors, urls = [], []
        page.on('pageerror', lambda e: errors.append(str(e)))
        page.on('request', lambda r: urls.append(r.url))
        page.goto(f'/g/{grant_id}')
        expect(page.locator('#grid .tile')).to_have_count(1)
        page.wait_for_function('document.querySelector("#grid img")?.naturalWidth > 0')
        page.locator('#grid .tile button').click()
        expect(page.locator('#viewer')).to_be_visible()
        page.wait_for_function('document.querySelector("#large")?.naturalWidth > 0')
        assert not any('/original/' in url for url in urls), 'browsing fetched original'
        meta = guest.request.get(f'/g/{grant_id}/meta').json()
        assert meta['left'] == 4, meta
        page.locator('#close').click()
        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth')
        page.locator('#grid input[type="checkbox"]').check()
        with page.expect_download(timeout=30000) as download_info:
            page.locator('#selected').click()
        body = Path(download_info.value.path()).read_bytes()
        z = zipfile.ZipFile(io.BytesIO(body))
        assert len(z.namelist()) == 1
        assert z.read(z.namelist()[0]).startswith(b'\x89PNG')
        assert guest.request.get(f'/g/{grant_id}/meta').json()['left'] == 3
        with page.expect_download(timeout=30000) as download_info:
            page.locator('#all').click()
        zipfile.ZipFile(download_info.value.path()).testzip()
        assert guest.request.get(f'/g/{grant_id}/meta').json()['left'] == 2
        assert not errors, errors
        page.screenshot(path=f'/tmp/weazl-gallery-{backend}-390.png', full_page=True)
        print('PASS: account-free mobile gallery/viewer, derivative-only browsing, selected/full ZIP downloads and exact retry counts')
    finally:
        guest.close()


def smoke_photo_performance(context, page, base, backend, jump_ms):
    timings = []
    for _ in range(20):
        start = time.perf_counter()
        assert context.request.get('/api/v1/photos?limit=100').ok
        assert context.request.get('/api/photos/dates').ok
        timings.append((time.perf_counter() - start) * 1000)
    p95 = sorted(timings)[18]
    assert p95 < 200, timings
    cold, warm = [], []
    for n in range(25):
        start = time.perf_counter()
        if n < 5:
            page.reload()
        else:
            page.goto(base + '/#library')
            page.goto(base + '/#photos')
        expect(page.locator('.photo-grid .library-card')).to_have_count(5)
        (cold if n < 5 else warm).append((time.perf_counter() - start) * 1000)
    assert sorted(warm)[18] < 1000, warm
    assert page.locator('.photo-grid .library-card').count() <= 300
    requests = []
    page.on('request', lambda r: requests.append(r.url))
    cdp = context.new_cdp_session(page)
    cdp.send('Performance.enable')
    work = []
    page.set_viewport_size({'width': 390, 'height': 600})
    for i in range(20):
        before = {m['name']: m['value'] for m in cdp.send('Performance.getMetrics')['metrics']}
        page.evaluate('(n) => scrollTo(0, n % 2 ? 0 : document.body.scrollHeight)', i)
        page.wait_for_timeout(80)
        after = {m['name']: m['value'] for m in cdp.send('Performance.getMetrics')['metrics']}
        work.append((after['ScriptDuration'] - before['ScriptDuration']) * 1000)
    script_p95 = sorted(work)[18]
    assert script_p95 < 16.7, work
    assert not any('/original' in url for url in requests), requests
    report = {'backend': backend, 'photos': 5, 'api_pair_p95_ms': p95, 'date_jump_ms': jump_ms,
              'reload_median_ms': statistics.median(cold), 'warm_viewport_p95_ms': sorted(warm)[18],
              'scroll_script_work_p95_ms': script_p95, 'scroll_original_requests': 0}
    Path(f'/tmp/weazl-modal-{backend}-performance.json').write_text(json.dumps(report, indent=2))
    page.set_viewport_size({'width': 1440, 'height': 1000})
    print('MODAL PERFORMANCE:', json.dumps(report))


def smoke_photo_selection(context, page, base):
    page.goto(base + '/#photos')
    expect(page.locator('.photo-grid .photo-tile')).to_have_count(5)
    tile = page.locator('.photo-grid .photo-tile').first
    tile.hover()
    selector = tile.locator('[data-photo-select]')
    expect(selector).to_have_css('opacity', '1')
    selector.click()
    expect(page.locator('.selection-toolbar')).to_contain_text('1 selected')
    expect(page.locator('#modal')).not_to_be_visible()
    # Clicking the tile while selection is active toggles rather than opening.
    page.locator('.photo-grid [data-select-file]').nth(1).click()
    expect(page.locator('.selection-toolbar')).to_contain_text('2 selected')
    second_id=page.locator('[data-photo-select]').nth(1).get_attribute('data-photo-select')
    page.locator('.photo-grid [data-select-file]').nth(1).click()
    expect(page.locator('.selection-toolbar')).to_contain_text('1 selected')
    expect(page.locator('[data-photo-select="'+second_id+'"]')).to_have_attribute('aria-pressed','false')
    page.locator('.photo-grid [data-select-file]').nth(1).click()
    expect(page.locator('.selection-toolbar')).to_contain_text('2 selected')
    page.locator('[data-photo-visibility="hide"]').click()
    expect(page.locator('.photo-grid .photo-tile')).to_have_count(3)
    expect(page.locator('.selection-toolbar')).to_have_count(0)
    page.locator('[data-photos-mode="hidden"]').click()
    expect(page.locator('.photo-grid .photo-tile')).to_have_count(2)
    page.locator('[data-photo-select]').first.click(force=True)
    page.locator('.photo-grid [data-select-file]').nth(1).click()
    page.locator('[data-photo-visibility="unhide"]').click()
    expect(page.locator('.photo-grid .photo-tile')).to_have_count(0)
    page.locator('[data-photos-mode="all"]').click()
    expect(page.locator('.photo-grid .photo-tile')).to_have_count(5)
    page.locator('[data-photo-select]').first.click(force=True)
    page.locator('[data-photo-visibility="set_archived"]').click()
    expect(page.locator('.photo-grid .photo-tile')).to_have_count(4)
    page.locator('[data-photos-mode="archived"]').click()
    expect(page.locator('.photo-grid .photo-tile')).to_have_count(1)
    page.locator('[data-photo-select]').first.click(force=True)
    page.locator('[data-photo-visibility="unarchive"]').click()
    expect(page.locator('.photo-grid .photo-tile')).to_have_count(0)
    page.locator('[data-photos-mode="all"]').click()
    expect(page.locator('.photo-grid .photo-tile')).to_have_count(5)
    page.locator('[data-photo-select]').first.click(force=True)
    page.keyboard.press('Escape')
    expect(page.locator('.selection-toolbar')).to_have_count(0)
    # Touch users get a persistent, finger-sized selector.
    page.set_viewport_size({'width':390,'height':844})
    page.emulate_media(media='screen')
    selector=page.locator('[data-photo-select]').first
    selector.focus()
    expect(selector).to_have_css('opacity','1')
    page.keyboard.press('Space')
    expect(page.locator('.selection-toolbar')).to_contain_text('1 selected')
    bar=page.locator('.selection-toolbar').bounding_box()
    rail=page.locator('[data-time-track]').bounding_box()
    assert bar['x']+bar['width'] <= rail['x'], 'selection actions overlap the date rail'
    page.keyboard.press('Escape')
    page.set_viewport_size({'width':1440,'height':1000})
    print('PASS: hover/keyboard selection, bulk Hide/Unhide and Archive/Unarchive, selection-mode clicks and Escape')
