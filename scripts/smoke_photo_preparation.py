"""Failure and pause recovery checks using the existing four-photo fixture."""
import time
from playwright.sync_api import expect


def smoke_photo_preparation(context, page, post, png):
    headers = {'X-Weazl-Desk': '1', 'Content-Type': 'application/octet-stream'}

    def wait_status(want):
        for _ in range(160):
            state = context.request.get('/api/photos/preparation').json()
            if state.get('status') == want:
                return state
            time.sleep(.25)
        raise AssertionError(f'preparation wanted {want}: {state}')

    state = context.request.get('/api/photos/preparation').json()
    assert state['status'] == 'paused' and state['paused'], state
    path = '/api/library?path=Photos/smoke-broken.png'
    response = context.request.put(path, data=b'not an image', headers=headers)
    assert response.ok, response.text()
    page.reload()
    state = context.request.get('/api/photos/preparation').json()
    assert state['status'] == 'paused', state
    post('/api/photos/preparation', {'action': 'resume'})
    state = wait_status('partial')
    assert (state['total'], state['ready'], state['failed']) == (5, 4, 1), state
    page.locator('.photo-tools > summary').click()
    retry = page.locator('[data-prep-action="retry"]')
    expect(retry).to_be_visible(timeout=10000)
    retry.click()
    state = wait_status('partial')
    assert state['ready'] == 4 and state['failed'] == 1, state
    response = context.request.put(path, data=png, headers=headers)
    assert response.ok, response.text()
    post('/api/photos/preparation', {'action': 'resume'})
    state = wait_status('complete')
    assert state['ready'] == 5 and state['failed'] == 0, state
    print('PASS: manual pause survives restart/index mutation; corrupt photo remains partial; retry button works; replacement recovers')
