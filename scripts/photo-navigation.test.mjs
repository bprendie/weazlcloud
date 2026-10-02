import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mergePhotoPage} from '../mockup-ui/photo-pages.js';
import {photoGeometry} from '../mockup-ui/photo-scroll.js';
const items=(start,count)=>Array.from({length:count},(_,i)=>({id:String(start+i)}));
const page=(start,count=120)=>({start,total:4000,generation:1,previous_cursor:start?'before-'+start:'',next_cursor:start+count<4000?'after-'+(start+count):''});
test('bidirectional pages retain both boundary cursors and evict whole pages',()=>{
 const state={photoItems:[]};mergePhotoPage(state,page(1920),items(1920,120),true,'next');
 for(let start=2040;start<=3960;start+=120)mergePhotoPage(state,page(start),items(start,Math.min(120,4000-start)),false,'next');
 assert.ok(state.photoItems.length<=2000);assert.equal(state.photoPreviousCursor,'before-'+state.photoStart);
 for(let start=1920;start>=0;start-=120)mergePhotoPage(state,page(start),items(start,120),false,'before');
 assert.equal(state.photoStart,0);assert.equal(state.photoPreviousCursor,'');assert.ok(state.photoItems.length<=2000);
 assert.equal(state.photoCursor,state.photoPages.at(-1).next);assert.equal(new Set(state.photoItems.map(x=>x.id)).size,state.photoItems.length);
 for(let i=1;i<state.photoItems.length;i++)assert.equal(Number(state.photoItems[i].id),Number(state.photoItems[i-1].id)+1);
});
test('duplicate reply merges once; incompatible generation cannot pollute a window',()=>{
 const state={photoItems:[]};mergePhotoPage(state,page(120),items(120,120),true,'next');mergePhotoPage(state,page(120),items(120,120),false,'next');assert.equal(state.photoItems.length,120);
 assert.throws(()=>mergePhotoPage(state,{...page(240),generation:2},items(240,120),false,'next'),/library changed/);assert.equal(state.photoItems.length,120);
});
test('virtual geometry leaves unloaded space on both sides and bounds huge collections',()=>{
 for(const total of [4000,5_000_000]){
  const state={photoItems:items(1800,120),photoTotal:total,photoStart:1800,photoDateSummary:{total,months:[{month:'2026-01',count:total}]}};
  const g=photoGeometry(state,1000,10000);assert.ok(g.before>0);assert.ok(g.after>0);assert.ok(g.before+g.after+10000<=8_000_001);
  assert.ok(g.rankAt(g.before/2)<1800);assert.ok(g.rankAt(g.before+10000+g.after/2)>1920);
 }
});
