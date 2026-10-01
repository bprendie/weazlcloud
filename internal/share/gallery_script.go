package share

const galleryScript = `
$=s=>document.querySelector(s); let items=[],session='',sessionAt=0,page=0,index=-1,generation=0,renewing=null;
const urls=new Map(),picked=new Set(),controllers=new Set(); let queue=[],active=0;
async function post(route,body={}) {
 const controller=new AbortController();controllers.add(controller);
 try {const r=await fetch(base+route,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({session,...body}),cache:'no-store',signal:controller.signal});
 if(!r.ok){const j=await r.json().catch(()=>({}));const e=new Error(j.error||'Gallery request failed');e.status=r.status;throw e;}return r;
 }finally{controllers.delete(controller);}
}
async function ensureSession(){
 if(!session)throw new Error('Open the gallery again.');
 if(Date.now()-sessionAt<10*60*1000)return;
 if(!renewing)renewing=post('gallery').then(r=>r.json()).then(j=>{session=j.session;sessionAt=Date.now();}).finally(()=>renewing=null);
 await renewing;
}
async function call(route,body={}){await ensureSession();return post(route,body);}
function releaseImages(){generation++;queue=[];observer.disconnect();for(const c of controllers)c.abort();controllers.clear();for(const url of urls.values())URL.revokeObjectURL(url);urls.clear();$('#large').removeAttribute('src');}
function fail(e){if(e.name==='AbortError')return;$('#error').textContent=e.message;if(e.status===401||e.status===404){releaseImages();session='';$('#all').hidden=$('#selected').hidden=true;$('#viewer').close();$('#grid').replaceChildren();$('#gate').hidden=false;$('#note').textContent='The session expired or this link is no longer available.';}}
async function open(phrase=''){
 try {releaseImages();picked.clear();page=0;const j=await(await post('gallery',{session:'',passphrase:phrase})).json();items=j.gallery.items;session=j.session;sessionAt=Date.now();$('#gate').hidden=true;$('#title').textContent=j.gallery.title;$('#note').textContent=items.length+' photos and videos';$('#error').textContent='';$('#all').hidden=false;render();}
 catch(e){if(e.status===401){$('#gate').hidden=false;$('#note').textContent='Enter the gallery passphrase.';}else{fail(e);}}
}
function trimURLs(){const keep=new Set(items.slice(page*60,page*60+60).map(i=>i.id));if(index>=0)keep.add(items[index]?.id);for(const [id,url]of urls){if(urls.size<=120)break;if(!keep.has(id)){URL.revokeObjectURL(url);urls.delete(id);}}}
async function preview(item,epoch=generation){if(!item?.preview_type)return '';if(urls.has(item.id))return urls.get(item.id);const r=await call('preview/'+item.id);const blob=await r.blob();if(epoch!==generation)return '';const url=URL.createObjectURL(blob);if(urls.has(item.id))URL.revokeObjectURL(urls.get(item.id));urls.set(item.id,url);trimURLs();return url;}
const observer=new IntersectionObserver(entries=>{for(const e of entries){if(e.isIntersecting){observer.unobserve(e.target);queue.push(e.target);pump();}}},{rootMargin:'300px'});
function pump(){while(active<3&&queue.length){const img=queue.shift();if(!img.isConnected)continue;const item=items.find(i=>i.id===img.dataset.id),epoch=generation;active++;preview(item,epoch).then(url=>{if(epoch===generation&&img.isConnected&&url)img.src=url;}).catch(fail).finally(()=>{active--;pump();});}}
function render(){
 generation++;queue=[];observer.disconnect();$('#grid').replaceChildren();const start=page*60,end=Math.min(start+60,items.length);
 for(let n=start;n<end;n++){const item=items[n],tile=document.createElement('article');tile.className='tile';const button=document.createElement('button');button.type='button';button.setAttribute('aria-label','View '+item.name);const img=document.createElement('img');img.alt=item.preview_type?'':item.media_type.startsWith('video/')?'Video preview unavailable':'Preview unavailable';img.dataset.id=item.id;button.append(img);button.onclick=()=>view(n);const label=document.createElement('label'),check=document.createElement('input');check.type='checkbox';check.checked=picked.has(item.id);check.setAttribute('aria-label','Select '+item.name);check.onchange=()=>{check.checked?picked.add(item.id):picked.delete(item.id);selectionLabel();};label.append(check);const name=document.createElement('p');name.textContent=item.name;tile.append(button,label,name);$('#grid').append(tile);if(item.preview_type)observer.observe(img);}
 $('#more').hidden=end===items.length;$('#page-prev').hidden=page===0;$('#page-number').textContent=items.length?(start+1)+'–'+end+' of '+items.length:'';selectionLabel();trimURLs();
}
function selectionLabel(){$('#selected').hidden=!picked.size;$('#selected').textContent='Download selected ('+picked.size+')';}
async function view(n){index=n;const item=items[n];$('#name').textContent=item.name;$('#prev').disabled=n===0;$('#next').disabled=n===items.length-1;$('#large').removeAttribute('src');$('#large').alt=item.preview_type?'':'Preview unavailable; download the original to view it.';if(!$('#viewer').open)$('#viewer').showModal();try{const url=await preview(item);if(index===n&&url)$('#large').src=url;}catch(e){fail(e);}}
async function download(route,ids=[]){
 try {await ensureSession();if(ids.length){$('#note').textContent='Preparing selected ZIP…';let job=await(await call('zip/jobs',{ids})).json();while(job.status==='queued'||job.status==='preparing'){await new Promise(r=>setTimeout(r,1000));job=await(await call('zip/jobs/'+job.id)).json();}if(job.status!=='ready')throw new Error(job.error||'ZIP preparation failed');route='zip/jobs/'+job.id+'/download';}
 const form=document.createElement('form');form.method='POST';form.action=base+route;form.target='download-target';const input=document.createElement('input');input.type='hidden';input.name='session';input.value=session;form.append(input);document.body.append(form);form.submit();form.remove();$('#note').textContent='Download requested. Each explicit transfer uses one retry, including an interrupted transfer.';
 }catch(e){fail(e);}
}
$('#gate').onsubmit=e=>{e.preventDefault();const phrase=new FormData(e.target).get('passphrase');e.target.reset();open(phrase);};$('#all').onclick=()=>download('zip');$('#selected').onclick=()=>download('zip',[...picked]);$('#original').onclick=()=>download('original/'+items[index].id);$('#more').onclick=()=>{page++;render();};$('#page-prev').onclick=()=>{page=Math.max(0,page-1);render();};$('#close').onclick=()=>$('#viewer').close();$('#prev').onclick=()=>view(Math.max(0,index-1));$('#next').onclick=()=>view(Math.min(items.length-1,index+1));
document.addEventListener('keydown',e=>{if(!$('#viewer').open)return;if(e.key==='ArrowLeft'&&index>0)view(index-1);if(e.key==='ArrowRight'&&index+1<items.length)view(index+1);});
$('iframe').onload=()=>{try{const text=$('iframe').contentDocument.body.textContent.trim();if(text)$('#error').textContent=text;}catch{}};
document.addEventListener('visibilitychange',()=>{if(!document.hidden&&session)ensureSession().catch(fail);});
window.addEventListener('pagehide',()=>{releaseImages();items=[];picked.clear();session='';});open();
`
