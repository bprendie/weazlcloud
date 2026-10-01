import {test} from 'node:test';
import assert from 'node:assert/strict';
import {layoutPhotos, photoAspect, photoRowWindow} from '../mockup-ui/photo-layout.js';

test('justified rows preserve aspect ratios, date boundaries and container bounds', () => {
  const items = Array.from({length:2000}, (_,id) => ({id:`p${id}`, width:id%3 ? 3000 : 2000, height:2000, captureTime:`2024-01-${String(30-Math.floor(id/100)).padStart(2,'0')}T12:00:00Z`}));
  for (const width of [320, 900, 1920]) {
    const layout = layoutPhotos(items,width);
    assert.equal(layout.rows.filter(row=>!row.tiles.length).length,20);
    assert.equal(layout.rows.reduce((n,row)=>n+row.tiles.length,0),2000);
    for (const row of layout.rows.filter(row=>row.tiles.length)) {
      const total = row.tiles.reduce((n,tile)=>n+tile.width,0)+8*(row.tiles.length-1);
      assert.ok(total <= width+0.001);
      for (const tile of row.tiles) assert.ok(Math.abs(tile.width/row.height-photoAspect(items[tile.index]))<0.001);
    }
    for (let top=0;top<layout.height;top+=700) {
      const visible = photoRowWindow(layout,top,700);
      assert.ok(visible.tiles<=300);
      assert.ok(visible.rows.length>0);
      assert.ok(visible.top + visible.bottom <= layout.height);
    }
  }
});

test('portrait orientation and unavailable dimensions keep stable placeholders', () => {
  assert.equal(photoAspect({width:3000,height:2000,orientation:6}),2/3);
  assert.equal(photoAspect({}),1);
  const items=[{id:'unknown'}];
  assert.equal(layoutPhotos(items,320).rows[0].day,'unknown');
  assert.equal(layoutPhotos(items,320),layoutPhotos(items,320));
});


test('recently added headings use import dates while the timeline keeps capture dates',()=>{
 const items=[{id:'p1',captureTime:'2013-01-02T12:00:00Z',importedAt:'2026-09-30T12:00:00Z'}];
 assert.equal(layoutPhotos(items,320,'comfortable','recent').rows[0].day,'2026-09-30');
 assert.equal(layoutPhotos(items,320,'comfortable','capture').rows[0].day,'2013-01-02');
});
