// Session-only compressed bytes. Mounted consumers hold a lease on their URL.
export class PhotoBlobCache {
  constructor({bytes=32*1024*1024,entries=256,fetcher=fetch,urls=URL}={}) {
    Object.assign(this,{limit:bytes,max:entries,fetcher,urls});
    this.failures=new Map();this.items=new Map();this.pending=new Map();this.bytes=0;this.generation=0;this.hits=0;
  }
  discard(key,item) { this.items.delete(key);this.bytes-=item.bytes;this.urls.revokeObjectURL(item.url); }
  space(bytes) {
    for(const [key,item] of this.items) {
      if(this.bytes+bytes<=this.limit && this.items.size<this.max)break;
      if(!item.refs)this.discard(key,item);
    }
    return this.bytes+bytes<=this.limit && this.items.size<this.max;
  }
  lease(key,item) {
    this.items.delete(key);this.items.set(key,item);item.refs++;
    let released=false;
    return {url:item.url,release:()=>{if(!released){released=true;item.refs--;}}};
  }
  async acquire(key,url,{signal}={}) {
    if(signal?.aborted)throw new DOMException('Canceled','AbortError');
    const failed=this.failures.get(key);if(failed?.until>Date.now())throw failed.error;this.failures.delete(key);
    const cached=this.items.get(key);
    if(cached){this.hits++;return this.lease(key,cached);}
    let request=this.pending.get(key);
    if(!request){
      const controller=new AbortController(),generation=this.generation;
      request={controller,users:0};
      request.promise=(async()=>{
        const response=await (0,this.fetcher)(url,{signal:controller.signal,cache:'no-store'});
        if(!response.ok){
         const error=new Error(response.status===413?'Preview exceeds server limits':response.status>=500?'Preview temporarily unavailable':'Preview unavailable');
         if(this.failures.size>=this.max)this.failures.delete(this.failures.keys().next().value);
         if(generation===this.generation)this.failures.set(key,{error,until:Date.now()+(response.status>=500?10000:60000)});
         throw error;
        }
        const blob=await response.blob();
        if(controller.signal.aborted || generation!==this.generation)throw new DOMException('Canceled','AbortError');
        if(!['image/jpeg','image/png'].includes(blob.type) || !this.space(blob.size))throw new Error('Preview cache is full');
        const item={url:this.urls.createObjectURL(blob),bytes:blob.size,refs:0};
        this.items.set(key,item);this.bytes+=blob.size;return item;
      })().finally(()=>{if(this.pending.get(key)===request)this.pending.delete(key);});
      this.pending.set(key,request);
    }
    request.users++;
    let abort;
    const canceled=new Promise((_,reject)=>{abort=()=>reject(new DOMException('Canceled','AbortError'));signal?.addEventListener('abort',abort,{once:true});});
    try {
      const item=await Promise.race([request.promise,canceled]);
      if(signal?.aborted || this.items.get(key)!==item)throw new DOMException('Canceled','AbortError');
      return this.lease(key,item);
    } finally {signal?.removeEventListener('abort',abort);if(--request.users===0)this.pending.get(key)?.controller.abort();}
  }
  clear() {
    this.generation++;this.failures.clear();
    for(const request of this.pending.values())request.controller.abort();
    this.pending.clear();
    for(const [key,item] of this.items)this.discard(key,item);
  }
}
