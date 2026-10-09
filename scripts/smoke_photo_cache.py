"""Use the existing small collection to measure private browser reuse."""
import json
from pathlib import Path

def smoke_photo_cache(context,page,base,backend):
    page.goto(base+'/#photos')
    page.wait_for_function("""() => {const pane=document.querySelector('#content.photos-scrollport').getBoundingClientRect();const images=[...document.querySelectorAll('.photo-grid [data-photo-thumbnail]')].filter(img=>{const r=img.getBoundingClientRect();return r.bottom>pane.top && r.top<pane.bottom;});return images.length && images.every(img=>img.complete&&img.naturalWidth);}""")
    requests=[]
    listener=lambda r:requests.append(r.url) if '/thumbnail?' in r.url else None
    page.on('request',listener)
    try:
        report=page.evaluate("""async()=>{
          const {renderMain}=await import('/views.js'),{photoBlobCache}=await import('/photo-images.js');
          const before=photoBlobCache.hits,times=[];
          const kept=[...document.querySelectorAll('.photo-grid [data-photo-thumbnail]')].filter(img=>img.complete&&img.naturalWidth).map(img=>({img,src:img.src}));
          for(let i=0;i<30;i++){
            const start=performance.now();renderMain();
            await new Promise((resolve,reject)=>{const poll=()=>{
              const pane=document.querySelector('#content.photos-scrollport').getBoundingClientRect();const images=[...document.querySelectorAll('.photo-grid [data-photo-thumbnail]')].filter(img=>{const r=img.getBoundingClientRect();return r.bottom>pane.top && r.top<pane.bottom;});
              if(images.length && images.every(img=>img.complete&&img.naturalWidth)){requestAnimationFrame(resolve);return;}
              if(performance.now()-start>5000){reject(Error('warm preview did not paint'));return;}
              requestAnimationFrame(poll);
            };requestAnimationFrame(poll);});
            times.push(performance.now()-start);
            if(!kept.every(({img,src})=>img.isConnected && img.src===src && img.naturalWidth))throw Error('unchanged refresh replaced a decoded thumbnail');
          }
          return {samples:times.length,preserved_images:kept.length,warm_grid_p95_ms:times.sort((a,b)=>a-b)[28],browser_hits:photoBlobCache.hits-before,retained_bytes:photoBlobCache.bytes,retained_entries:photoBlobCache.items.size};
        }""")
        assert report['preserved_images']>0,report
        assert report['retained_bytes']<=32*1024*1024 and report['retained_entries']<=256,report
        assert not requests,requests
        report['repeat_thumbnail_requests']=len(requests)
        assert report['warm_grid_p95_ms']<500,report
        page.screenshot(path=f'/tmp/weazl-photos-toolbar-{backend}.png')
        Path(f'/tmp/weazl-photo-cache-{backend}-performance.json').write_text(json.dumps(report,indent=2))
        print('PHOTO CACHE PERFORMANCE:',json.dumps(report))
    finally:
        page.remove_listener('request',listener)
