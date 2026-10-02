// Retain whole pages: boundary cursors always describe the actual retained range.
export function mergePhotoPage(state,page,incoming,reset,direction) {
 const pages=reset?[]:[...(state.photoPages||[])];
 const start=page.start ?? (direction==='before'?Math.max(0,(state.photoStart||0)-incoming.length):(state.photoStart||0)+state.photoItems.length);
 if(!reset && state.photoGeneration && page.generation!==state.photoGeneration)throw new Error('photo library changed; reload the page');
 const record={start,items:incoming,previous:page.previous_cursor||'',next:page.next_cursor||''};
 const existing=pages.findIndex(p=>p.start===start);if(existing>=0)pages[existing]=record;else pages.push(record);
 pages.sort((a,b)=>a.start-b.start);
 let count=pages.reduce((n,p)=>n+p.items.length,0);
 while(count>2000 && pages.length>1){const removed=direction==='before'?pages.pop():pages.shift();count-=removed.items.length;}
 const seen=new Set(),items=[];
 for(const p of pages)for(const item of p.items)if(!seen.has(item.id)){seen.add(item.id);items.push(item);}
 state.photoPages=pages;state.photoItems=items;state.photoStart=pages[0]?.start||0;
 state.photoPreviousCursor=pages[0]?.previous||'';state.photoCursor=pages.at(-1)?.next||'';state.photoHasMore=Boolean(state.photoCursor);
 state.photoGeneration=page.generation||0;state.photoTotal=page.total ?? state.photoTotal;
}
