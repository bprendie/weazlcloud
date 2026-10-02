// One scroll owner for Photos; Library continues using the document.
export const photoPane = () => document.querySelector('#content.photos-scrollport');
export const viewScrollTop = () => photoPane()?.scrollTop ?? window.scrollY;
export const viewScrollTo = options => (photoPane() || window).scrollTo(options);
export const viewScrollBy = options => (photoPane() || window).scrollBy(options);
export const photoViewport = () => ({top:photoPane()?.getBoundingClientRect().top || 0,height:photoPane()?.clientHeight || innerHeight});

export function sizePhotoPane() {
 const pane=photoPane();if(!pane)return;
 const top=pane.getBoundingClientRect().top;
 const bottom=document.querySelector('.deck')?.getBoundingClientRect().top ?? innerHeight;
 const height=Math.max(180,bottom-top-6);
 const value=`${height}px`;
 if(pane.style.getPropertyValue('--photo-pane-height')!==value)pane.style.setProperty('--photo-pane-height',value);
 pane.style.setProperty('--photo-pane-top',`${top}px`);
}

// Estimated date-group space remains on both sides of the retained pages.
// Compress only unloaded space to avoid browser CSS-height limits.
export function photoGeometry(state,width,loadedHeight=0) {
 const target=state.photoDensity==='compact'?140:210;
 const perRow=Math.max(1,width/(target+8)),unit=(target+8)/perRow;
 const total=state.photoTotal ?? state.photoDateSummary?.total ?? state.photoItems.length;
 const start=state.photoStart||0,end=Math.min(total,start+state.photoItems.length);
 const buckets=state.photosMode==='recent'?[]:state.photoDateSummary?.months||[];
 const groups=[];let rank=0,height=0;
 for(const b of buckets){const h=b.count*unit+Math.min(30,b.count)*44;groups.push({rank,count:b.count,top:height,height:h});rank+=b.count;height+=h;}
 if(rank<total){const count=total-rank;groups.push({rank,count,top:height,height:count*unit+44});height+=count*unit+44;}
 const at=position=>{const g=groups.find(g=>position<g.rank+g.count)||groups.at(-1);return g?g.top+Math.max(0,Math.min(g.count,position-g.rank))/g.count*g.height:0;};
 const scale=Math.min(1,Math.max(1,8_000_000-loadedHeight)/Math.max(1,height));
 const before=at(start)*scale,after=Math.max(0,height-at(end))*scale;
 return {before,after,total,start,end,rankAt(y){
   const estimate=y<before?y/scale:(y-before-loadedHeight)/scale+at(end);
   const g=groups.find(g=>estimate<g.top+g.height)||groups.at(-1);
   return Math.max(0,Math.min(total-1,g?Math.floor(g.rank+(estimate-g.top)/g.height*g.count):0));
 }};
}
