// A scroll or drag that starts on a tile is never a selection click. Keyboard
// activation still works, and the next pointer press starts a fresh gesture.
export function installPhotoGestures(root=document) {
  let press=null;
  root.addEventListener('pointerdown',event=>{
    press=event.target.closest('.photo-tile')?{id:event.pointerId,x:event.clientX,y:event.clientY,scroll:document.querySelector('#content')?.scrollTop,moved:false}:null;
  },{passive:true});
  root.addEventListener('pointermove',event=>{
    if(press?.id===event.pointerId && Math.hypot(event.clientX-press.x,event.clientY-press.y)>8)press.moved=true;
  },{passive:true});
  root.addEventListener('pointercancel',event=>{if(press?.id===event.pointerId)press.moved=true;},{passive:true});
  root.addEventListener('click',event=>{
    const dragged=press && (press.moved || Math.abs((document.querySelector('#content')?.scrollTop||0)-press.scroll)>8);
    press=null;
    if(event.detail && dragged && event.target.closest('.photo-tile')){event.preventDefault();event.stopImmediatePropagation();}
  },true);
}
