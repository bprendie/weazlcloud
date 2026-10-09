export function zoomPhotoViewer(stage, zoom) {
  if(!stage)return;
  const x=(stage.scrollLeft+stage.clientWidth/2)/Math.max(1,stage.scrollWidth);
  const y=(stage.scrollTop+stage.clientHeight/2)/Math.max(1,stage.scrollHeight);
  stage.style.setProperty('--photo-canvas-scale',Math.max(1,zoom));
  stage.style.setProperty('--photo-media-scale',Math.min(1,zoom));
  stage.dataset.zoomed=String(zoom>1);
  // Read the new scroll extent before restoring the same point under the centre.
  stage.scrollLeft=x*stage.scrollWidth-stage.clientWidth/2;
  stage.scrollTop=y*stage.scrollHeight-stage.clientHeight/2;
}

export function installPhotoPan(stage) {
  let drag=null;
  stage.addEventListener('pointerdown',event=>{
    if(stage.dataset.zoomed!=='true' || event.pointerType!=='mouse' || event.button!==0 || event.target.closest('button,a,video[controls]'))return;
    drag={id:event.pointerId,x:event.clientX,y:event.clientY,left:stage.scrollLeft,top:stage.scrollTop};
    stage.setPointerCapture(event.pointerId);stage.dataset.panning='true';event.preventDefault();
  });
  stage.addEventListener('pointermove',event=>{
    if(drag?.id!==event.pointerId)return;
    stage.scrollLeft=drag.left+drag.x-event.clientX;
    stage.scrollTop=drag.top+drag.y-event.clientY;
  });
  const stop=()=>{drag=null;delete stage.dataset.panning;};
  stage.addEventListener('pointerup',stop);
  stage.addEventListener('pointercancel',stop);
  stage.addEventListener('lostpointercapture',stop);
}
