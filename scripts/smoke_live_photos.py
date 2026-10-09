"""Generated Live Photo resources through ordinary imports, repair and sharing."""
import base64
import json
import struct
import subprocess
import time
from urllib.parse import quote, urlparse
from playwright.sync_api import expect


def generated_live_media(image):
    def ffmpeg(args):
        return subprocess.check_output(['docker', 'run', '--rm', '--network', 'none',
            '--entrypoint', 'ffmpeg', image, '-v', 'error', *args])
    still = ffmpeg(['-f', 'lavfi', '-i', 'color=blue:s=32x24', '-frames:v', '1',
                    '-threads', '1', '-f', 'image2pipe', '-vcodec', 'mjpeg', 'pipe:1'])
    # Synthetic Apple maker note, tag 0x11; no personal photograph or metadata.
    identifier = b'weazl-fixture-pair\0'
    note = b'Apple iOS\0' + b'\x00\x01MM' + struct.pack('>H', 1)
    note += struct.pack('>HHII', 0x11, 2, len(identifier), 32) + b'\0'*4 + identifier
    exif = b'Exif\0\0' + b'II' + struct.pack('<HI', 42, 8) + struct.pack('<H', 1)
    exif += struct.pack('<HHII', 0x8769, 4, 1, 26) + b'\0'*4 + struct.pack('<H', 1)
    exif += struct.pack('<HHII', 0x927c, 7, len(note), 44) + b'\0'*4 + note
    still = still[:2] + b'\xff\xe1' + struct.pack('>H', len(exif)+2) + exif + still[2:]
    args = ['-f', 'lavfi', '-i', 'color=blue:s=32x24:r=10:d=2', '-threads', '1', '-c:v', 'libx264']
    end = ['-movflags', 'frag_keyframe+empty_moov+use_metadata_tags', '-f', 'mov', 'pipe:1']
    motion = ffmpeg(args + ['-metadata', 'com.apple.quicktime.content.identifier=weazl-fixture-pair'] + end)
    ordinary = ffmpeg(args + end)
    return still, motion, ordinary


def smoke_live_photos(context, page, post, browser, base, image, restart):
    still, motion, ordinary = generated_live_media(image)
    def upload(path, body):
        r = context.request.put('/api/library?path='+quote(path), data=body,
              headers={'X-Weazl-Desk':'1', 'Content-Type':'application/octet-stream'})
        assert r.ok, r.text()
    for name, body in [('still.jpg',still), ('motion.mov',motion), ('short.mov',ordinary)]:
        upload('Photos/live-smoke/'+name, body)
    def wait_job(min_total=0):
        for _ in range(160):
            job = context.request.get('/api/v1/photos/live-jobs').json()
            if job['status'].startswith('complete') and job['total'] >= min_total:
                return job
            time.sleep(.1)
        raise AssertionError(job)
    post('/api/v1/photos/live-jobs', {'action':'dry-run'})
    job = wait_job()
    assert job['pairable'] == 1 and job['paired'] == 0, job
    post('/api/v1/photos/live-jobs', {'action':'pause'})
    restart()
    assert context.request.get('/api/v1/photos/live-jobs').json()['status'] == 'paused'
    post('/api/v1/photos/live-jobs', {'action':'start'})
    job = wait_job()
    assert job['paired'] == 1, job
    items = context.request.get('/api/v1/photos?limit=100').json()['items']
    primary = next(x for x in items if x['path']=='Photos/live-smoke/still.jpg')
    assert not any(x['path']=='Photos/live-smoke/motion.mov' for x in items)
    assert any(x['path']=='Photos/live-smoke/short.mov' for x in items)
    assert len(primary['components']) == 2, primary
    for name, body in [('still.jpg',still), ('motion.mov',motion)]:
        r=context.request.get('/api/library?path='+quote('Photos/live-smoke/'+name))
        assert r.ok and r.body()==body, (name, r.status)
    r=context.request.get('/api/v1/photos/assets/'+primary['id']+'/motion', headers={'Range':'bytes=0-15'})
    assert r.status==206 and len(r.body())==16 and r.headers['content-type']=='video/mp4', r.text()
    page.goto(base+'/#photos')
    page.locator('[data-select-file="'+primary['id']+'"]').click()
    expect(page.locator('[data-action="photo-live-play"]')).to_be_visible()
    expect(page.locator('[data-live-motion]')).to_be_hidden()
    expect(page.locator('[data-action="photo-live-play"]')).to_have_text('Loop motion')
    def check_zoom(selector):
        media=page.locator(selector)
        before=media.bounding_box()
        page.evaluate('(selector)=>{window.zoomMedia=document.querySelector(selector);}',selector)
        page.get_by_role('button',name='Zoom in',exact=True).click()
        expect(page.locator('.photo-viewer-zoom')).to_have_text('150%')
        enlarged=media.bounding_box()
        assert enlarged['width']>before['width']*1.4 and enlarged['height']>before['height']*1.4,(before,enlarged)
        assert page.evaluate('(selector)=>window.zoomMedia===document.querySelector(selector)',selector)
        pan=page.locator('.photo-viewer-stage')
        initial=pan.evaluate('(el)=>({x:el.scrollLeft,y:el.scrollTop})')
        box=pan.bounding_box();x=box['x']+box['width']/2;y=box['y']+box['height']/2
        page.mouse.move(x,y);page.mouse.down();page.mouse.move(x+90,y+60,steps=5);page.mouse.up()
        moved=pan.evaluate('(el)=>({x:el.scrollLeft,y:el.scrollTop})')
        assert moved['x']<initial['x']-40 and moved['y']<initial['y']-30,(initial,moved)
        page.get_by_role('button',name='Fit photo to window').click()
        page.get_by_role('button',name='Zoom out',exact=True).click()
        expect(page.locator('.photo-viewer-zoom')).to_have_text('50%')
        smaller=media.bounding_box()
        assert smaller['width']<before['width']*.6 and smaller['height']<before['height']*.6,(before,smaller)
        page.get_by_role('button',name='Fit photo to window').click()
        expect(page.locator('.photo-viewer-zoom')).to_have_text('100%')
    page.wait_for_function('()=>document.querySelector("[data-photo-viewer-image]")?.naturalWidth>0')
    check_zoom('[data-photo-viewer-image]')
    page.evaluate("""() => {
      const video=document.querySelector('[data-live-motion]');
      window.liveLoopCount=0;let previous=0;
      video.addEventListener('timeupdate',()=>{if(video.currentTime<previous)window.liveLoopCount++;previous=video.currentTime;});
    }""")
    page.locator('[data-action="photo-live-play"]').click()
    page.wait_for_function('()=>document.querySelector("[data-live-motion]")?.currentTime>0')
    page.evaluate('()=>{window.liveVideo=document.querySelector("[data-live-motion]");}')
    check_zoom('[data-live-motion]')
    page.get_by_role('button',name='Zoom in',exact=True).click()
    refreshed=[]
    page.on('response',lambda r:refreshed.append(r.status) if '/api/v1/photos/seek' in r.url else None)
    upload('Photos/live-refresh.png',base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII='))
    for _ in range(100):
        if refreshed:break
        page.wait_for_timeout(100)
    assert refreshed, 'live upload did not refresh Photos'
    page.wait_for_timeout(1000)
    assert page.evaluate('()=>window.liveVideo.isConnected && window.liveVideo===document.querySelector("[data-live-motion]") && !window.liveVideo.paused'), 'upload replaced/stopped Live Photo playback'
    expect(page.locator('.photo-viewer-zoom')).to_have_text('150%')
    page.wait_for_function('()=>window.liveLoopCount>0',timeout=15000)
    expect(page.locator('[data-live-motion]')).to_be_visible()
    expect(page.locator('[data-action="photo-live-play"]')).to_have_attribute('aria-pressed','true')
    page.locator('[data-action="photo-live-play"]').click()
    expect(page.locator('[data-live-motion]')).to_be_hidden()
    assert page.locator('[data-live-motion]').evaluate('(v)=>v.paused')
    expect(page.locator('[data-action="photo-live-play"]')).to_have_attribute('aria-pressed','false')
    page.locator('[data-action="photo-viewer-close"]').click()
    grant=post('/api/v1/photos/grabs', {'ids':[primary['id']], 'gate':'open', 'grabs':3, 'expiry':'1d'})
    guest_base=f'http://{urlparse(base).hostname}:{urlparse(base).port+1}'
    guest=browser.new_context(base_url=guest_base)
    try:
        opened=guest.request.post('/g/'+grant['id']+'/gallery',data={}).json()
        child=next(x for x in opened['gallery']['items'] if x.get('parent_id'))
        frozen=guest.request.post('/g/'+grant['id']+'/motion/'+child['id'],data={'session':opened['session']})
        owner_motion=context.request.get('/api/v1/photos/assets/'+primary['id']+'/motion')
        assert frozen.ok and frozen.headers['content-type']=='video/mp4' and frozen.body()==owner_motion.body(), (frozen.status,frozen.headers,len(frozen.body()),len(owner_motion.body()))
        gp=guest.new_page();errors=[];gp.on('pageerror',lambda e:errors.append(str(e)))
        gp.on('console',lambda message: print('GUEST CONSOLE:',message.text,flush=True) if message.type=='error' else None)
        gp.goto('/g/'+grant['id'])
        expect(gp.locator('#grid .tile')).to_have_count(1)
        gp.locator('#grid .tile button').click()
        expect(gp.locator('#play-motion')).to_be_visible()
        gp.locator('#play-motion').click()
        try:
            gp.wait_for_function('()=>document.querySelector("#motion")?.currentTime>0',timeout=10000)
        except Exception:
            print('GUEST MOTION:',gp.evaluate('()=>{const v=document.querySelector("#motion");return {error:document.querySelector("#error").textContent,ready:v.readyState,src:v.src,mediaError:v.error?.message};}'),flush=True)
            raise
        meta=guest.request.get('/g/'+grant['id']+'/meta').json()
        assert meta['left']==3, meta
        assert not errors, errors
    finally:
        guest.close()
    # Late independently arriving resources are picked up by the durable queue.
    # Upload completion precedes the asynchronous ingest outbox. Waiting only
    # for "complete" can accept the previous pass before this file is queued.
    total=context.request.get('/api/v1/photos/live-jobs').json()['total']
    upload('Photos/live-late/still.jpg',still)
    wait_job(total+1)
    upload('Photos/live-late/motion.mov',motion)
    wait_job(total+2)
    items=context.request.get('/api/v1/photos?limit=100').json()['items']
    late=next(x for x in items if x['path']=='Photos/live-late/still.jpg')
    assert len(late['components'])==2, late
    print('PASS: Apple/QuickTime identifier repair, dry-run/pause/restart, still/motion zoom, pan, Fit, looping through live uploads, Stop, Range, frozen guest motion, original bytes and late imports',flush=True)
