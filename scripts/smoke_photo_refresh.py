"""Live upload DOM stability and portrait playback in a disposable library."""
import subprocess
from urllib.parse import quote
from playwright.sync_api import expect


def smoke_photo_refresh(context, page, base, png, image):
    page.goto(base + '/#photos')
    page.wait_for_function('''async()=>{const {state}=await import('/data.js');return !state.photoLoading && state.photoItems.length>0;}''')
    page.wait_for_function('''()=>[...document.querySelectorAll('.photo-grid img')].some(img=>img.complete && img.naturalWidth>0)''')
    page.wait_for_timeout(1200)
    page.evaluate('''()=>{
        window.keptImages=[...document.querySelectorAll('.photo-grid img')].filter(img=>img.complete&&img.naturalWidth>0).map(img=>({img,src:img.src,key:img.closest('.photo-tile').dataset.photoPreviewKey}));
        window.blankImages=[];
        window.previewObserver=new MutationObserver(records=>{for(const r of records)if(keptImages.some(x=>x.img===r.target)&&!r.target.getAttribute('src'))blankImages.push(r.target);});
        previewObserver.observe(document.querySelector('#content'),{subtree:true,attributes:true,attributeFilter:['src']});
    }''')
    requests=[]
    def record(req):
        if '/thumbnail' in req.url:requests.append(req.url)
    page.on('request', record)
    try:
        r=context.request.put('/api/library?path=Photos/refresh-smoke.png',data=png,
              headers={'X-Weazl-Desk':'1','Content-Type':'application/octet-stream'})
        assert r.ok,r.text()
        page.wait_for_function('''async()=>{const {state}=await import('/data.js');return !state.photoLoading && state.photoItems.some(x=>x.path==='Photos/refresh-smoke.png');}''')
        page.wait_for_timeout(1500)
        status=page.evaluate('''()=>({kept:keptImages.length,blank:blankImages.length,stable:keptImages.every(({img,src})=>img.isConnected&&img.src===src&&img.naturalWidth>0),ids:keptImages.map(({img})=>img.dataset.photoThumbnail),details:keptImages.map(({img,src,key})=>({key,connected:img.isConnected,same:img.src===src,loaded:img.naturalWidth,currentKey:document.querySelector(`[data-photo-thumbnail="${img.dataset.photoThumbnail}"]`)?.closest('.photo-tile')?.dataset.photoPreviewKey}))})''')
        assert status['kept'] and status['blank']==0 and status['stable'],status
        assert not any('/'+id+'/thumbnail' in url for id in status['ids'] for url in requests), requests
    finally:
        page.remove_listener('request', record)
        page.evaluate('previewObserver.disconnect()')

    # Gesture cancellation must not leave selection active or open a viewer.
    page.locator('[data-action="photos-selection-start"]').click()
    page.locator('.photo-tile').first.evaluate('''el=>{
        const button=el.querySelector('[data-select-file]');
        button.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true,pointerId:1,pointerType:'touch',clientX:50,clientY:50}));
        button.dispatchEvent(new PointerEvent('pointermove',{bubbles:true,pointerId:1,pointerType:'touch',clientX:50,clientY:150}));
        button.dispatchEvent(new PointerEvent('pointercancel',{bubbles:true,pointerId:1,pointerType:'touch'}));
        button.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true,detail:1}));
    }''')
    expect(page.locator('.selection-toolbar')).to_contain_text('0 selected')
    page.locator('[data-action="photos-selection-cancel"]').click()

    movie=subprocess.check_output(['docker','run','--rm','--network','none','--entrypoint','sh',image,'-c',
        'ffmpeg -v error -f lavfi -i color=purple:size=180x320:rate=10:duration=2 -c:v libx264 -threads 1 -pix_fmt yuv420p /tmp/portrait.mov && cat /tmp/portrait.mov'])
    r=context.request.put('/api/library?path='+quote('Photos/portrait-smoke.mov'),data=movie,
                          headers={'X-Weazl-Desk':'1','Content-Type':'application/octet-stream'})
    assert r.ok,r.text()
    page.wait_for_function('''async()=>{const {state}=await import('/data.js');return !state.photoLoading&&state.photoItems.some(x=>x.path==='Photos/portrait-smoke.mov');}''')
    tile=page.locator('[data-drag-file="Photos/portrait-smoke.mov"]')
    tile.scroll_into_view_if_needed()
    page.wait_for_function('''()=>document.querySelector('[data-drag-file="Photos/portrait-smoke.mov"] img')?.naturalWidth>0''')
    tile.locator('[data-select-file]').click()
    video=page.locator('.photo-viewer-stage video')
    expect(video).to_be_visible()
    page.wait_for_function('''()=>document.querySelector('.photo-viewer-stage video')?.videoHeight===320''')
    for width,height in [(1440,1000),(390,844),(844,390)]:
        page.set_viewport_size({'width':width,'height':height})
        bounds=video.evaluate('''el=>{const r=el.getBoundingClientRect(),s=el.closest('.photo-viewer-stage').getBoundingClientRect(),c=document.querySelector('.photo-viewer-caption').getBoundingClientRect();return {top:r.top,bottom:r.bottom,left:r.left,right:r.right,stageTop:s.top,caption:c.top,fit:getComputedStyle(el).objectFit,scroll:el.closest('.photo-viewer-stage').scrollHeight,stageHeight:s.height};}''')
        assert bounds['top']>=bounds['stageTop'] and bounds['bottom']<=bounds['caption']+1 and bounds['right']<=width and bounds['left']>=0 and bounds['fit']=='contain' and bounds['scroll']<=bounds['stageHeight']+1,bounds
    page.locator('[data-action="photo-viewer-close"]').click()
    page.set_viewport_size({'width':1440,'height':1000})
    print('PASS: live upload retains decoded images/URLs without refetch, drag guard, MOV poster and portrait video fit at desktop/phone/landscape sizes',flush=True)
