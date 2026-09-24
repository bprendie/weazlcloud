"""Music portion of smoke-photo-albums.py; uses synthetic, committed audio."""
from pathlib import Path
from urllib.parse import quote
from playwright.sync_api import expect


def smoke_music(context, page, post):
    fixtures = Path(__file__).resolve().parent.parent/'internal/music/testdata'
    headers = {'X-Weazl-Desk': '1', 'Content-Type': 'application/octet-stream'}
    for ext in ['mp3', 'flac', 'm4a', 'ogg', 'opus']:
        path = f'Music/tagged.{ext}'
        response = context.request.put('/api/library?path='+quote(path), data=(fixtures/f'tagged.{ext}').read_bytes(), headers=headers)
        assert response.ok, response.text()
        response = context.request.get('/api/library/music?path='+quote(path))
        assert response.ok, response.text()
        tags = response.json()
        assert tags['title'] == 'Midnight <Signal>', tags
        assert tags['artist'] == 'Weazl Test Artist', tags
        assert tags['album'] == 'Purple Test Album', tags
        assert tags['artwork'].startswith('data:image/png;base64,'), tags
    response = context.request.put('/api/library?path=Music/untagged.mp3', data=b'no usable music tags', headers=headers)
    assert response.ok, response.text()
    page.locator('nav [data-view="library"]').click()
    page.locator('[data-library-view="grid"]').click()
    page.locator('[data-open-folder="Music"]').click()
    expect(page.locator('.grid-audio')).to_have_count(6)
    expect(page.locator('[data-music-cover]:visible')).to_have_count(5, timeout=30000)
    expect(page.locator('[data-music-details]:visible')).to_have_count(5)
    expect(page.locator('[data-music-details] strong').first).to_have_text('Midnight <Signal>')
    assert page.locator('[data-music-details] signal').count() == 0, 'metadata interpreted as HTML'
    assert page.locator('[data-grid-music="Music/untagged.mp3"] .grid-music-art span').is_visible()
    # Genuine MP3 playback, then single-player behavior with FLAC.
    first = page.locator('audio[data-media-path="Music/tagged.mp3"]')
    second = page.locator('audio[data-media-path="Music/tagged.flac"]')
    first.evaluate('(a) => { a.muted = true; return a.play(); }')
    page.wait_for_timeout(300)
    assert first.evaluate('(a) => a.currentTime > 0 && !a.paused')
    second.evaluate('(a) => { a.muted = true; return a.play(); }')
    assert first.evaluate('(a) => a.paused'), 'two players active'
    second.evaluate('(a) => a.pause()')
    assert page.locator('.grid-audio').first.evaluate('(el) => el.scrollWidth <= el.clientWidth'), 'card overflow'
    page.screenshot(path='/tmp/weazl-music-grid-smoke.png', full_page=True)
    post('/api/lock', {})
    assert not context.request.get('/api/library/music?path=Music/tagged.mp3').ok, 'locked tags exposed'
    post('/api/unlock', {'passphrase':'album-test-pass'})
    post('/api/users', {'username':'music-other','password':'other-music-pass','vault_passphrase':'other-music-pass'})
    other = context.browser.new_context(base_url=page.url.split('/#')[0])
    try:
        for path, data in [('/api/login', {'username':'music-other','password':'other-music-pass'}), ('/api/unlock', {'passphrase':'other-music-pass'})]:
            response = other.request.post(path, data=data, headers={'X-Weazl-Desk':'1'})
            assert response.ok, response.text()
        assert not other.request.get('/api/library/music?path=Music/tagged.mp3&owner=albums').ok, 'cross-owner tags exposed'
    finally:
        other.close()
    print('PASS: MP3/FLAC/M4A/Ogg/Opus tags + artwork, grid, playback, fallback, locked/other-owner isolation')
