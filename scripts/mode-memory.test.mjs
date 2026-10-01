import {test} from 'node:test';
import assert from 'node:assert/strict';
import {ModeMemory} from '../mockup-ui/mode-memory.js';

test('mode switches restore independent selections and scroll without aliasing',()=>{
  const memory = new ModeMemory();
  const state = {username:'owner',unlocked:true,selectedFiles:['file'],selected:{id:'file'},selectionAnchor:'file',photoSelectedItems:new Map()};
  memory.remember('library',state,1000);
  state.selectedFiles.push('other'); state.photoSelection={id:'token',count:32000};
  memory.remember('photos',state,2000,{id:'photo',top:20});
  assert.deepEqual(memory.restore('library',state),{scroll:1000,anchor:null});
  assert.deepEqual(state.selectedFiles,['file']); assert.equal(state.photoSelection,null);
  assert.equal(memory.restore('photos',state).anchor.id,'photo'); assert.equal(state.photoSelection.count,32000);
  memory.clearSelections(); memory.restore('photos',state); assert.equal(state.selectedFiles.length,0); assert.equal(state.photoSelection,null);
});

test('vault locks and account changes cannot restore another session',()=>{
  const memory=new ModeMemory(),state={username:'a',unlocked:true,selectedFiles:['private']};
  memory.remember('photos',state,20);
  state.username='b'; assert.equal(memory.restore('photos',state),null);
  state.username='a'; state.unlocked=false; assert.equal(memory.restore('photos',state),null);
  memory.clear(); state.unlocked=true; assert.equal(memory.restore('photos',state),null);
});


test('route changes cannot restore a selection from another folder or filter',()=>{
 const memory=new ModeMemory(),state={username:'a',unlocked:true,currentPath:'folder-a',photosMode:'all',selectedFiles:['id']};
 memory.remember('library',state,20);state.currentPath='folder-b';assert.equal(memory.restore('library',state),null);
 memory.remember('photos',state,50);state.photosMode='hidden';assert.equal(memory.restore('photos',state),null);
});
