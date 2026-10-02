"""Continuous rail and metadata job smoke, reusing the existing five photos."""
import time
import json
import os
import subprocess
from pathlib import Path
from urllib.parse import quote
from playwright.sync_api import expect


def smoke_photo_timeline(context, page, post, base):
    page.goto(base + '/#photos')
    expect(page.locator('.photo-grid .library-card')).to_have_count(5)
    items = context.request.get('/api/v1/photos?limit=10').json()['items']
    for item, date in zip(items[:3], ['2024-06-07T12:00:00Z', '2018-06-15T12:00:00Z', '2011-01-02T12:00:00Z']):
        post('/api/v1/photos/assets/' + item['id'], {'captured_at': date, 'offset_known': False})
    page.reload()
    rail = page.locator('[data-time-track]')
    expect(rail).to_be_visible()
    expect(page.locator('[data-time-rank]').filter(has_text='2018')).to_be_visible()
    expect(page.locator('[data-action="photo-metadata"]').first).to_be_hidden()
    page.locator('.photo-tools > summary').click()
    expect(page.locator('[data-metadata-action="dry-run"]')).to_be_visible()
    page.keyboard.press('Escape')
    expect(page.locator('[data-metadata-action="dry-run"]')).to_be_hidden()
    bounds = rail.bounding_box()
    assert page.evaluate('document.documentElement.clientWidth') - bounds['x'] - bounds['width'] >= 30
    # The reserved rail column also protects toolbar controls from interception.
    for selector in ('.photos-toolbar', '.photo-search-filters'):
        box = page.locator(selector).bounding_box()
        if box:
            assert box['x'] + box['width'] <= bounds['x'], (selector, box, bounds)
    seeks = []
    page.on('request', lambda r: seeks.append(r.url) if '/api/v1/photos/seek' in r.url else None)
    page.locator('[data-time-rank]').filter(has_text='2018').click()
    expect(rail).to_have_attribute('aria-valuetext', 'June 2018')
    expect(page.locator('.photo-grid .library-card')).to_have_count(5)
    page.locator(f'.photo-grid [data-select-file="{items[1]["id"]}"]').click()
    expect(page.locator('#modal.photo-viewer')).to_be_visible()
    page.locator('[data-action="photo-viewer-close"]').click()
    expect(rail).to_have_attribute('aria-valuetext', 'June 2018')
    rail.focus()
    rail.press('Home')
    expect(rail).to_have_attribute('aria-valuetext', 'June 2024')
    wait_idle(page)
    rail.press('End')
    expect(rail).to_have_attribute('aria-valuetext', 'January 2011')
    wait_idle(page)
    page.go_back()
    expect(rail).to_have_attribute('aria-valuetext', 'June 2024')
    wait_idle(page)
    page.go_back()
    expect(rail).to_have_attribute('aria-valuetext', 'June 2018')
    wait_idle(page)
    seeks.clear()
    bounds = rail.bounding_box()
    tick = page.locator('[data-time-rank]').filter(has_text='2018').bounding_box()
    page.mouse.move(tick['x'] + tick['width']/2, tick['y'] + tick['height']/2)
    page.mouse.down()
    page.mouse.move(bounds['x'] + bounds['width'] - 3, bounds['y'] + bounds['height'] - 4, steps=20)
    assert len(seeks) == 0, 'drag fetched intermediate windows'
    page.mouse.up()
    expect(rail).to_have_attribute('aria-valuetext', 'Unknown date')
    assert len(seeks) == 1, seeks
    page.locator('[data-time-calendar]').click()
    page.locator('[data-time-date]').fill('2018-06-15')
    expect(rail).to_have_attribute('aria-valuetext', 'June 2018')
    wait_idle(page)
    page.locator('nav [data-view="library"]').click()
    post('/api/v1/photos/assets/'+items[1]['id'],{'caption':'Timeline anchor fixture'})
    page.wait_for_function("async () => {const {state}=await import('/data.js');return state.photoModeDirty;}")
    page.locator('nav [data-view="photos"]').click()
    expect(rail).to_have_attribute('aria-valuetext','June 2018')
    wait_idle(page)
    inspected = post('/api/v1/photos/metadata-jobs', {'action': 'dry-run', 'sidecars_only': True})
    for _ in range(100):
        inspected = context.request.get('/api/v1/photos/metadata-jobs').json()
        if inspected['status'].startswith('complete'):
            break
        time.sleep(.05)
    else:
        raise AssertionError(inspected)
    assert inspected['total'] == 5
    assert inspected['updated'] == 0, inspected
    report = context.request.get('/api/v1/photos/metadata-jobs?report=1').json()
    assert len(report['entries']) == 5
    smoke_cancelled_seek(page, rail)
    smoke_delayed_summary_history(page, rail)
    measure_rail(context, page)
    page.set_viewport_size({'width': 390, 'height': 844})
    page.reload()
    expect(rail).to_be_visible()
    wait_idle(page)
    expect(rail).to_be_visible()
    page.wait_for_function('() => document.querySelector("[data-time-track]")?.getBoundingClientRect().width >= 44')
    # The renderer can replace the rail between Playwright's visibility and
    # bounding-box calls; inspect the mounted element atomically instead.
    rail_bounds(page)
    assert page.evaluate('() => document.documentElement.scrollWidth <= innerWidth'), 'mobile rail overflow'
    smoke_touch(context, page, rail, seeks)
    smoke_sidecar_ingest(context, post)
    page.set_viewport_size({'width': 1440, 'height': 1000})
    page.goto(base + '/#photos')
    print('PASS: continuous date rail, one release request, exact day, keyboard, mobile geometry and encrypted job report')


def wait_idle(page):
    page.wait_for_function("async () => {const {state}=await import('/data.js');return !state.photoLoading && !state.photoJumpAnchor;}")


def smoke_delayed_summary_history(page, rail):
    wait_idle(page)
    held = []
    def hold_once(route):
        if not held:
            held.append((route, route.fetch()))
        else:
            route.continue_()
    page.route('**/api/v1/photos/dates?**', hold_once)
    try:
        rail.focus()
        rail.press('Home')
        expect(rail).to_have_attribute('aria-valuetext', 'June 2024')
        page.wait_for_timeout(100)
        assert held, 'summary was not delayed'
        # A second accepted page must not erase the first jump's history entry.
        rail.press('End')
        expect(rail).to_have_attribute('aria-valuetext', 'January 2011')
        wait_idle(page)
        for route, response in held:
            route.fulfill(response=response)
        page.go_back()
        expect(rail).to_have_attribute('aria-valuetext', 'June 2024')
        wait_idle(page)
    finally:
        page.unroute('**/api/v1/photos/dates?**', hold_once)
    print('PASS: accepted jumps retain Back history while a date summary is delayed')


def measure_rail(context, page):
    samples, api = [], []
    rail=page.locator('[data-time-track]')
    page.evaluate("() => {window.__railFrames={running:true,values:[]};let last;const next=t=>{if(!window.__railFrames.running)return;if(last!==undefined)window.__railFrames.values.push(t-last);last=t;requestAnimationFrame(next)};requestAnimationFrame(next)}")
    for i in range(20):
        year, label = ('2024','June 2024') if i % 2 else ('2018','June 2018')
        start=time.perf_counter()
        page.locator('[data-time-rank]').filter(has_text=year).click()
        expect(rail).to_have_attribute('aria-valuetext',label)
        wait_idle(page)
        samples.append((time.perf_counter()-start)*1000)
        start=time.perf_counter()
        assert context.request.get('/api/v1/photos/seek?at=2018-06-15').ok
        assert context.request.get('/api/v1/photos/dates').ok
        api.append((time.perf_counter()-start)*1000)
    active_frames=page.evaluate('() => {window.__railFrames.running=false;return window.__railFrames.values}')
    frames=page.evaluate("""() => new Promise(resolve=>{let last,values=[];const next=t=>{if(last!==undefined)values.push(t-last);last=t;if(values.length===60)resolve(values);else requestAnimationFrame(next)};requestAnimationFrame(next);})""")
    result={'fixture_photos':5,'release_to_cards_p95_ms':sorted(samples)[18], 'seek_summary_http_pair_p95_ms':sorted(api)[18], 'idle_animation_frame_p95_ms':sorted(frames)[56], 'jump_animation_frame_p95_ms':sorted(active_frames)[min(len(active_frames)-1,int(len(active_frames)*.95))]}
    containers=subprocess.check_output(['docker','ps','--filter','name=weazl-albums-','--format','{{.Names}}'],text=True).splitlines()
    if len(containers)==1:
        processes=subprocess.check_output(['docker','top',containers[0],'-eo','pid,rss,comm'],text=True).splitlines()[1:]
        result['sample_node_process_rss_kib']=sum(int(line.split()[1]) for line in processes)
        result['sample_processes']=[line.split()[2] for line in processes]
    backend=os.environ.get('WEAZLCLOUD_SMOKE_STORAGE_BACKEND','restic')
    Path(f'/tmp/weazl-timeline-{backend}-performance.json').write_text(json.dumps(result,indent=2))
    print('TIMELINE PERFORMANCE:',json.dumps(result))


def smoke_touch(context,page,rail,seeks):
    wait_idle(page)
    cdp=context.new_cdp_session(page)
    cdp.send('Emulation.setTouchEmulationEnabled',{'enabled':True,'maxTouchPoints':1})
    bounds=rail_bounds(page);x=bounds['x']+bounds['width']-3
    seeks.clear()
    point=lambda y:[{'x':x,'y':y}]
    cdp.send('Input.dispatchTouchEvent',{'type':'touchStart','touchPoints':point(bounds['y']+bounds['height']*.1)})
    expect(page.locator('.photo-time-rail.dragging')).to_be_visible()
    for step in range(1,11):
        cdp.send('Input.dispatchTouchEvent',{'type':'touchMove','touchPoints':point(bounds['y']+bounds['height']*(.1+.08*step))})
    assert not seeks, 'touch drag fetched intermediate pages'
    cdp.send('Input.dispatchTouchEvent',{'type':'touchEnd','touchPoints':[]})
    expect(rail).to_have_attribute('aria-valuetext','Unknown date')
    wait_idle(page)
    assert len(seeks)==1,seeks
    before=page.evaluate('() => scrollY')
    cdp.send('Input.dispatchTouchEvent',{'type':'touchStart','touchPoints':[{'x':200,'y':650}]})
    for y in [600,500,400,300]:
        cdp.send('Input.dispatchTouchEvent',{'type':'touchMove','touchPoints':[{'x':200,'y':y}]})
        page.wait_for_timeout(30)
    cdp.send('Input.dispatchTouchEvent',{'type':'touchEnd','touchPoints':[]})
    page.wait_for_function('(before) => scrollY > before',arg=before)
    cdp.send('Emulation.setTouchEmulationEnabled',{'enabled':False})
    print('PASS: Chromium touch rail releases once; ordinary grid touch scrolling remains enabled')


def smoke_sidecar_ingest(context,post):
    items=context.request.get('/api/v1/photos?limit=10').json()['items']
    unknown=[item for item in items if not item.get('captured_at')]
    assert len(unknown)==2
    before={item['id']:context.request.get('/api/v1/photos/assets/'+item['id']+'/original').body() for item in items}
    for item,raw in zip(unknown,[json.dumps({'title':unknown[0]['path'].rsplit('/',1)[-1],'photoTakenTime':{'timestamp':'1365152400'}}).encode(),b'{broken']):
        result=context.request.put('/api/library?path='+quote(item['path']+'.supplemental-metadata.json'),data=raw,headers={'X-Weazl-Desk':'1','Content-Type':'application/octet-stream'})
        assert result.ok,result.text()
    for _ in range(200):
        state=context.request.get('/api/v1/photos/metadata-jobs').json()
        if state['status'].startswith('complete') and state['failed']==1:break
        time.sleep(.05)
    else:raise AssertionError(state)
    after=context.request.get('/api/v1/photos?limit=10').json()['items']
    assert sum(bool(item.get('captured_at')) for item in after)==4,after
    assert next(item for item in after if item['id']==unknown[0]['id'])['capture_source']=='takeout-photoTakenTime'
    for item in after:
        assert context.request.get('/api/v1/photos/assets/'+item['id']+'/original').body()==before[item['id']]
    post('/api/v1/photos/metadata-jobs',{'action':'start','sidecars_only':True})
    for _ in range(200):
        state=context.request.get('/api/v1/photos/metadata-jobs').json()
        if state['status'].startswith('complete'):break
        time.sleep(.05)
    else:raise AssertionError(state)
    assert state['updated']==0 and state['unchanged']==4 and state['failed']==1,state
    print('PASS: late sidecar automatically repairs date, corrupt neighbor does not stop it, second apply is idempotent, original bytes preserved')


def smoke_cancelled_seek(page,rail):
    held=[]
    def hold(route):
        if 'rank=1' in route.request.url:
            held.append((route,route.fetch()))
        else:route.continue_()
    page.route('**/api/v1/photos/seek?**',hold)
    try:
        page.locator('[data-time-rank]').filter(has_text='2018').click()
        page.wait_for_timeout(100)
        assert held,'seek was not held'
        page.locator('[data-time-rank]').filter(has_text='2024').click()
        expect(rail).to_have_attribute('aria-valuetext','June 2024')
        wait_idle(page)
        for route,response in held:
            try:route.fulfill(response=response)
            except Exception:pass  # An aborted Chromium route may already be closed.
        page.wait_for_timeout(100)
        expect(rail).to_have_attribute('aria-valuetext','June 2024')
    finally:page.unroute('**/api/v1/photos/seek?**',hold)
    print('PASS: a delayed obsolete date seek cannot replace the newer destination')


def rail_bounds(page):
    for _ in range(40):
        value=page.evaluate("() => {const e=document.querySelector('[data-time-track]');if(!e)return null;const r=e.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height}}")
        if value and value['width'] >= 43.99 and value['height'] > 0:return value
        page.wait_for_timeout(50)
    raise AssertionError('mobile rail did not stabilize')
