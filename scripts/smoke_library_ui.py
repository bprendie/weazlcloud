"""Authenticated Library regression checks on the disposable album fixture."""
from playwright.sync_api import expect


def smoke_library_ui(page, backend):
    page.locator('nav [data-view="library"]').click()
    page.locator('[data-library-path=""]').first.click()
    expect(page.locator('.library-action-bar')).to_be_visible()
    expect(page.locator('.queue-panel')).to_have_count(0)
    expect(page.locator('.library-breadcrumb .crumb-sep')).to_have_count(0)
    assert 'A file is present or it is not.' not in page.locator('#content').inner_text()
    for action in ['upload', 'upload-folder', 'new-folder']:
        expect(page.locator(f'[data-action="{action}"]')).to_be_visible()
    filters = page.locator('[data-library-filters]')
    filters.focus()
    page.keyboard.press('Enter')
    expect(page.locator('.library-filter-menu')).to_be_visible()
    page.locator('[data-library-filter="type"]').select_option('image')
    expect(page.locator('[data-library-clear-filter="type"]')).to_be_visible()
    page.locator('[data-library-clear-filter="type"]').click()
    expect(page.locator('[data-library-clear-filter]')).to_have_count(0)
    page.locator('[data-library-filters]').click()
    page.locator('#upload').set_input_files({
        'name': 'upload-' + 'long-name-' * 15 + '.txt',
        'mimeType': 'text/plain', 'buffer': b'UI smoke upload\n' * 200,
    })
    tray = page.locator('#upload-tray')
    expect(tray).to_be_visible()
    expect(tray.locator('strong').first).to_have_text('1 of 1 uploaded', timeout=30000)
    page.locator('[data-upload-collapse]').click()
    expect(tray.locator('.upload-rails')).to_have_count(0)
    page.locator('nav [data-view="photos"]').click()
    expect(tray).to_be_visible()
    page.locator('nav [data-view="library"]').click()
    for width in [1440, 390]:
        page.set_viewport_size({'width': width, 'height': 1000})
        expect(page.locator('[data-action="upload"]')).to_be_visible()
        assert tray.evaluate('(el) => el.scrollWidth <= el.clientWidth'), 'upload tray overflow'
        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'), 'page overflow'
        page.screenshot(path=f'/tmp/weazl-library-{backend}-{width}.png', full_page=True)
    page.locator('[data-dismiss-upload]').click()
    expect(tray).not_to_be_visible()
    page.set_viewport_size({'width': 1440, 'height': 1000})
    print('PASS: Library toolbar, keyboard filters/chips, real upload, floating tray across navigation, desktop/mobile overflow')
