"""Metadata-only multi-page wheel regression; no large fixture uploads."""
import base64
import json
import time
from urllib.parse import urlparse, parse_qs
from playwright.sync_api import expect


def smoke_bidirectional_photos(page, base):
    page.set_viewport_size({'width':1440,'height':1000})
    total = 4000
    delayed = {'checked':False,'armed':False,'route':None}
    png = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=')
    def seek(route):
        q = parse_qs(urlparse(route.request.url).query)
        limit = 120
        cursor = q.get('cursor', [''])[0]
        if cursor.startswith('b:'):
            end = int(cursor[2:]); start = max(0, end-limit)
        elif cursor.startswith('n:'):
            start = int(cursor[2:]); end = min(total, start+limit)
        else:
            around=q.get('around',[''])[0]
            target=int(around.split('-')[1]) if around.startswith('wheel-') else int(q.get('rank',['0'])[0])
            rank = max(0, min(total-1, target))
            start = max(0, rank-limit//2); end = min(total, start+limit)
        rows = [{'id': f'wheel-{i:05d}', 'path': f'Photos/wheel-{i:05d}.png', 'revision': 1,
                 'size': len(png), 'media_type': 'image/png', 'width': 32, 'height': 24,
                 'captured_at': '2020-01-02T12:00:00Z'} for i in range(start, end)]
        anchor = q.get('rank', [str(target if not cursor else start)])[0]
        body=json.dumps({
            'items': rows, 'generation': 999, 'start': start, 'position': int(anchor), 'total': total,
            'anchor_id': f'wheel-{int(anchor):05d}', 'previous_cursor': f'b:{start}' if start else '',
            'next_cursor': f'n:{end}' if end<total else ''})
        if cursor and delayed['armed'] and not delayed['checked']:
            delayed.update(checked=True,route=route,body=body,direction=-1 if cursor.startswith('b:') else 1)
            return
        route.fulfill(status=200,content_type='application/json',body=body)
    def continue_pending_wheel():
        if not delayed['route']:return
        # Continue scrolling with a pending response, then release it after
        # wheel delivery settles. No nested input calls inside route handlers.
        page.mouse.wheel(0,120*delayed['direction'])
        page.wait_for_timeout(150)
        continued=page.evaluate('()=>{const pane=document.querySelector("#content").getBoundingClientRect();const el=[...document.querySelectorAll(".photo-grid [data-select-file]")].find(el=>el.getBoundingClientRect().bottom>pane.top+1);return el?{id:el.dataset.selectFile,top:el.getBoundingClientRect().top}:null;}')
        assert continued,'pending-page wheel left no visible anchor'
        delayed['route'].fulfill(status=200,content_type='application/json',body=delayed['body'])
        delayed['route']=None
        page.wait_for_function("async()=>{const {state}=await import('/data.js');return !state.photoLoading;}")
        page.wait_for_timeout(100)
        restored=page.evaluate('(id)=>{const el=document.querySelector(`[data-select-file="${id}"]`);return el?{y:el.getBoundingClientRect().y}:null;}',continued['id'])
        assert restored and abs(restored['y']-continued['top'])<=2,(continued,restored)

    def dates(route):
        route.fulfill(status=200, content_type='application/json', body=json.dumps({
            'total': total, 'known_dates': total, 'unknown_dates': 0, 'generation': 999,
            'months': [{'month': '2020-01', 'count': total, 'rank': 0}]}))
    def thumbnail(route):
        if 'wheel-' in route.request.url:
            route.fulfill(status=200, content_type='image/png', body=png)
        else:
            route.continue_()
    page.route('**/api/v1/photos/seek*', seek)
    page.route('**/api/v1/photos/dates*', dates)
    page.route('**/api/v1/photos/assets/**/thumbnail*', thumbnail)
    try:
        page.goto(base+'/#photos')
        page.wait_for_function("async()=>{const {state}=await import('/data.js');return state.photoTotal===4000 && !state.photoLoading;}")
        # Returning from a long Library page must not leave a second document
        # scroll offset behind the Photos pane or put its toolbar offscreen.
        page.locator('nav [data-view="library"]').click()
        page.wait_for_function('()=>!document.querySelector("#content").classList.contains("photos-scrollport")')
        page.wait_for_function("async()=>{const {state}=await import('/data.js');return state.view==='library' && !state.libraryLoading;}")
        page.evaluate('()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))')
        page.evaluate('()=>{const spacer=document.createElement("div");spacer.style.height="2600px";document.querySelector("#content").append(spacer);window.scrollTo(0,600);}')
        assert page.evaluate('scrollY')>0
        page.locator('nav [data-view="photos"]').click()
        page.wait_for_function("async()=>{const {state}=await import('/data.js');return state.photoTotal===4000 && !state.photoLoading;}")
        geometry=page.evaluate('()=>{const p=document.querySelector("#content").getBoundingClientRect(),f=document.querySelector(".deck").getBoundingClientRect();return {scroll:scrollY,doc:document.documentElement.scrollHeight,viewport:innerHeight,top:p.top,bottom:p.bottom,footer:f.top};}')
        assert geometry['scroll']==0 and geometry['doc']<=geometry['viewport']+1 and geometry['top']>=0 and geometry['bottom']<=geometry['footer'],geometry
        rail = page.locator('[data-time-track]')
        expect(rail).to_be_visible()
        page.wait_for_function('()=>{const el=document.querySelector("[data-time-track]");return el && !el.closest(".photo-time-rail").hidden && el.getBoundingClientRect().height>0;}')
        box = page.evaluate('()=>{const r=document.querySelector("[data-time-track]").getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height};}')
        page.mouse.click(box['x']+box['width']-3,box['y']+box['height']*.45)
        page.wait_for_function("async()=>{const {state}=await import('/data.js');return state.photoStart>1000 && !state.photoLoading && !state.photoJumpAnchor;}")
        pane = page.locator('#content.photos-scrollport')
        assert pane.evaluate('(el)=>el.scrollTop') > 0
        initial = page.evaluate("async()=>{const {state}=await import('/data.js');return state.photoStart;}")
        delayed['armed']=True
        pane_box = pane.bounding_box()
        page.mouse.move(pane_box['x']+100,pane_box['y']+150)
        for _ in range(8):
            page.mouse.wheel(0,-700)
            page.wait_for_timeout(250)
            continue_pending_wheel()
        page.wait_for_function(f"async()=>{{const {{state}}=await import('/data.js');return state.photoStart<{initial} && !state.photoLoading;}}")
        earlier = page.evaluate("async()=>{const {state}=await import('/data.js');return state.photoPosition;}")
        for _ in range(12):
            page.mouse.wheel(0,700)
            page.wait_for_timeout(250)
            continue_pending_wheel()
        page.wait_for_function(f"async()=>{{const {{state}}=await import('/data.js');return state.photoPosition>{earlier} && !state.photoLoading;}}")
        snapshot = page.evaluate("async()=>{const {state}=await import('/data.js');return {ids:state.photoItems.map(x=>x.id),count:document.querySelectorAll('.photo-tile').length,previous:state.photoPreviousCursor,start:state.photoStart};}")
        assert len(snapshot['ids']) <= 2000 and len(set(snapshot['ids'])) == len(snapshot['ids']), snapshot
        assert snapshot['count'] <= 300, snapshot['count']
        assert delayed['checked'],'wheel did not cross a delayed page boundary'
        assert snapshot['previous'] == f"b:{snapshot['start']}" if snapshot['start'] else not snapshot['previous']
        # A real metadata change drives the normal event stream while page
        # replies remain synthetic. Refresh must retain the visible anchor.
        anchor=page.evaluate('()=>{const pane=document.querySelector("#content").getBoundingClientRect();const el=[...document.querySelectorAll(".photo-grid [data-select-file]")].find(el=>el.getBoundingClientRect().bottom>pane.top+1);return {id:el.dataset.selectFile,top:el.getBoundingClientRect().top};}')
        real=page.request.get('/api/v1/photos?limit=1').json()['items'][0]
        with page.expect_response(lambda r:'/api/v1/photos/seek' in r.url and 'around='+anchor['id'] in r.url):
            response=page.request.post('/api/v1/photos/assets/'+real['id'],data={'caption':'Scroll refresh fixture'},headers={'X-Weazl-Desk':'1'})
            assert response.ok,response.text()
        page.wait_for_function("async()=>{const {state}=await import('/data.js');return !state.photoLoading;}")
        page.wait_for_timeout(200)
        restored=page.locator('[data-select-file="'+anchor['id']+'"]').bounding_box()
        assert restored and abs(restored['y']-anchor['top'])<=2,(anchor,restored)
        assert page.evaluate('scrollY')==0
        print('PASS: metadata-only 4,000-photo seek, wheel up/down during pending page, mode scroll isolation, event refresh anchor and bounded rows', flush=True)
    finally:
        page.unroute('**/api/v1/photos/seek*', seek)
        page.unroute('**/api/v1/photos/dates*', dates)
        page.unroute('**/api/v1/photos/assets/**/thumbnail*', thumbnail)
        page.goto(base+'/#photos')
