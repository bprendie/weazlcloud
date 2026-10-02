import {PhotoBlobCache} from './photo-cache.js';
export const photoBlobCache=new PhotoBlobCache();
const mounted=new Map();
let scope='';
export function clearPhotoImages(){
  for(const [el,lease] of mounted){el.removeAttribute('src');lease.release();}
  mounted.clear();photoBlobCache.clear();scope='';
}
export function releaseDetachedPhotoImages(){
  for(const [el,lease] of mounted)if(!el.isConnected){lease.release();mounted.delete(el);}
}
export function photoImageScope(state){
  const next=state.authenticated && state.unlocked?`${state.username}\0${state.photosMode==='hidden'}`:'';
  if(scope!==next){clearPhotoImages();scope=next;}
  return scope;
}
export function photoImageKey(state,item,size){return `${photoImageScope(state)}\0${item?.previewIdentity || `${item?.id}:${item?.revision}:${item?.userRotation || 0}`}\0${size}`;}
export async function mountPhotoImage(el,state,item,size,signal){
  const owner=photoImageScope(state),key=photoImageKey(state,item,size);
  const id=item?.entryID || item?.id || el.dataset.photoThumbnail;
  const url=`/api/v1/photos/assets/${encodeURIComponent(id)}/thumbnail?size=${size}${state.photosMode==='hidden'?'&hidden=1':''}`;
  const lease=await photoBlobCache.acquire(key,url,{signal});
  if(!el.isConnected || signal?.aborted || owner!==photoImageScope(state) || !state.unlocked){lease.release();return;}
  mounted.get(el)?.release();mounted.set(el,lease);el.src=lease.url;
}
export async function prefetchPhotoImage(state,item,size){
  const id=item.entryID||item.id,key=photoImageKey(state,item,size);
  const lease=await photoBlobCache.acquire(key,`/api/v1/photos/assets/${encodeURIComponent(id)}/thumbnail?size=${size}${state.photosMode==='hidden'?'&hidden=1':''}`);
  lease.release();
}
