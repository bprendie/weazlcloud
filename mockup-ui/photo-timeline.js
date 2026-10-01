// Metadata-only rail geometry. No asset bytes or whole-library pages are needed.
export function timelineTarget(summary, fraction) {
  const total = summary?.total || 0;
  if (!total) return {rank:0, date:'unknown', label:'Unknown date'};
  const rank = Math.max(0, Math.min(total - 1, Math.round(Math.max(0, Math.min(1, fraction)) * (total - 1))));
  if (rank >= (summary.known_dates || 0)) return {rank, date:'unknown', label:'Unknown date'};
  let start = 0;
  for (const bucket of summary.months || []) {
    start += bucket.count;
    if (rank < start) return {rank, date:bucket.month, label:timelineLabel(bucket.month)};
  }
  return {rank,date:'unknown',label:'Unknown date'};
}
export function timelineLabel(date) {
  if (!date || date === 'unknown') return 'Unknown date';
  return new Date(`${date.length === 7 ? date + '-15' : date}T12:00:00Z`).toLocaleDateString(undefined,
    {year:'numeric',month:'long',...(date.length === 10 ? {day:'numeric'} : {}),timeZone:'UTC'});
}
export function timelineTicks(summary) {
  const ticks = []; let rank = 0, year = '';
  for (const bucket of summary?.months || []) {
    if (bucket.month.slice(0,4) !== year) { year = bucket.month.slice(0,4); ticks.push({year,rank}); }
    rank += bucket.count;
  }
  const stride = Math.max(1,Math.ceil(ticks.length/7));
  return ticks.filter((_,i) => i % stride === 0 || i === ticks.length-1);
}
export function timelineMarkup(state, esc) {
  const summary = state.photoDateSummary;
  if (!summary?.total || state.photosMode === 'recent' || state.photosMode === 'albums' && !state.photosAlbum) return '';
  const max = Math.max(0,summary.total-1), rank = Math.min(max,state.photoPosition || 0);
  const target = timelineTarget(summary,max ? rank/max : 0);
  const ticks = timelineTicks(summary).map(t => `<button type="button" class="photo-time-year" data-time-rank="${t.rank}" data-time-percent="${max ? t.rank/max*100 : 0}" aria-label="Jump to ${t.year}">${t.year}</button>`).join('');
  return `<aside class="photo-time-rail" aria-label="Photo timeline navigation">
    <button class="photo-time-calendar" data-time-calendar aria-label="Choose a capture date">⌁</button>
    <label class="photo-time-picker" hidden>Jump to date<input type="date" data-time-date aria-label="Jump to capture date"></label>
    <div class="photo-time-track" data-time-track role="slider" tabindex="0" aria-label="Photo date" aria-orientation="vertical" aria-valuemin="0" aria-valuemax="${max}" aria-valuenow="${rank}" aria-valuetext="${esc(target.label)}">
      ${ticks}<span class="photo-time-thumb"></span><span class="photo-time-badge" aria-hidden="true">${esc(target.label)}</span>
    </div>${summary.unknown_dates ? '<button type="button" class="photo-time-unknown" data-time-unknown aria-label="Jump to photos with unknown dates">?</button>' : ''}
  </aside>`;
}

export function installPhotoTimeline({state,summaryForMonth,jump}) {
  let drag = null, frame = 0, keyboardRequest = 0;
  const usable = () => state.view === 'photos' && state.unlocked && state.photosMode !== 'recent';
  const update = (track,fraction,visibleDate) => {
    const target = timelineTarget(state.photoDateSummary,fraction);
    if(visibleDate)target.label=timelineLabel(visibleDate);
    const max = Math.max(0,(state.photoDateSummary?.total || 1)-1);
    const percent = max ? target.rank/max*100 : 0;
    track.setAttribute('aria-valuenow',String(target.rank)); track.setAttribute('aria-valuetext',target.label);
    track.querySelector('.photo-time-thumb')?.style.setProperty('top',`${percent}%`);
    const badge = track.querySelector('.photo-time-badge');
    if (badge) { if(badge.textContent!==target.label)badge.textContent=target.label; badge.style.top=`${percent}%`; }
    return target;
  };
  const cancel = () => { const previous=drag;drag=null;if(previous){try{previous.track.releasePointerCapture(previous.id);}catch{}previous.track.closest('.photo-time-rail')?.classList.remove('dragging');} cancelAnimationFrame(frame); frame=0; };
  const finish = async target => { keyboardRequest++; await jump(target); };
  document.addEventListener('pointerdown',e => {
    const track=e.target.closest('[data-time-track]');
    const tick=e.target.closest('[data-time-rank]');
    if (!track || e.target.closest('button') && e.pointerType!=='touch' || !usable() || e.button !== 0) return;
    e.preventDefault(); cancel(); track.focus({preventScroll:true});
    track.setPointerCapture(e.pointerId); track.closest('.photo-time-rail').classList.add('dragging');
    const rect=track.getBoundingClientRect(); const fraction=(e.clientY-rect.top)/rect.height;
    drag={track,id:e.pointerId,y:e.clientY,startY:e.clientY,tapRank:tick?Number(tick.dataset.timeRank):null,target:update(track,fraction)};
  });
  document.addEventListener('pointermove',e => {
    if (!drag || e.pointerId!==drag.id) return;
    drag.y=e.clientY;
    if (!frame) frame=requestAnimationFrame(()=>{ frame=0; if (!drag) return; const rect=drag.track.getBoundingClientRect(); drag.target=update(drag.track,(drag.y-rect.top)/rect.height); });
  });
  document.addEventListener('pointerup',e => {
    if (!drag || e.pointerId!==drag.id) return;
    const rect=drag.track.getBoundingClientRect(),target=update(drag.track,(e.clientY-rect.top)/rect.height);
    const rank=drag.tapRank!==null && Math.abs(e.clientY-drag.startY)<6?drag.tapRank:target.rank;
    cancel(); if (usable()) finish({rank});
  });
  document.addEventListener('pointercancel',cancel);
  document.addEventListener('lostpointercapture',()=>{if(drag)cancel();});
  window.addEventListener('resize',cancel);
  document.addEventListener('click',e => {
    if (!usable()) return;
    const button=e.target.closest('[data-time-rank],[data-time-unknown],[data-time-calendar]'); if(!button)return;
    if (button.hasAttribute('data-time-calendar')) { const picker=button.parentElement.querySelector('.photo-time-picker'); picker.hidden=!picker.hidden; if(!picker.hidden)picker.querySelector('input').focus(); return; }
    finish(button.hasAttribute('data-time-unknown') ? {at:'unknown'} : {rank:Number(button.dataset.timeRank)});
  });
  document.addEventListener('change',e => { if(e.target.hasAttribute('data-time-date') && e.target.value && usable())finish({at:e.target.value}); });
  document.addEventListener('keydown',async e => {
    const track=e.target.closest('[data-time-track]'); if(!track || !usable())return;
    if(!['ArrowUp','ArrowDown','PageUp','PageDown','Home','End'].includes(e.key))return;
    e.preventDefault(); const token=++keyboardRequest;
    const summary=state.photoDateSummary,target=timelineTarget(summary,Number(track.getAttribute('aria-valuenow'))/Math.max(1,summary.total-1));
    const currentItem=state.photoItems[Number(track.getAttribute('aria-valuenow'))-(state.photoStart||0)];
    if(currentItem)target.date=currentItem.captureTime?.slice(0,7)||'unknown';
    if(e.key==='Home'){finish({rank:0});return;}
    if(e.key==='End'){finish({rank:Math.max(0,summary.known_dates-1)});return;}
    const direction=['ArrowDown','PageDown'].includes(e.key)?1:-1;
    const months=summary.months || [],current=months.findIndex(b=>b.month===target.date);
    if(target.date==='unknown'){if(direction<0 && months.length)finish({rank:months.at(-1).rank});return;}
    if(e.key.startsWith('Page') || target.date==='unknown'){
      const index=Math.max(0,Math.min(months.length-1,current+direction));
      if(months[index])finish({rank:months[index].rank});return;
    }
    const dates=await summaryForMonth(target.date);
    if(token!==keyboardRequest || !usable())return;
    const days=[...(dates?.days || [])].sort((a,b)=>a.rank-b.rank);
    const rank=Number(track.getAttribute('aria-valuenow'));
    const candidates=days.filter(d=>direction>0 ? d.rank>rank : d.rank<rank);
    const destination=direction>0 ? candidates[0] : candidates.at(-1);
    if(destination)finish({rank:destination.rank});
    else { const month=months[current+direction]; if(month) { const adjacent=await summaryForMonth(month.month); if(token!==keyboardRequest || !usable())return; const ranks=(adjacent.days||[]).map(d=>d.rank); finish({rank:direction>0?Math.min(...ranks):Math.max(...ranks)}); } }
  });
  const controller={
    sync() {
      const track=document.querySelector('[data-time-track]');
      if(!usable() || !track){cancel();return;}
      const rail=track.closest('.photo-time-rail'),tray=document.querySelector('.upload-tray:not([hidden])');
      const trayTop=tray?.getBoundingClientRect().top;
      rail.style.bottom=trayTop===undefined?'':`${Math.max(innerWidth<=700?160:170,innerHeight-trayTop+12)}px`;
      rail.hidden=trayTop!==undefined && trayTop-parseFloat(getComputedStyle(rail).top)<160;
      if(rail.hidden){cancel();return;}
      if(drag && (!drag.track.isConnected || !usable()))cancel();
      if(drag)return;
      let lastTick=-Infinity;const height=track.clientHeight;
      track.querySelectorAll('[data-time-percent]').forEach(tick=>{const top=Number(tick.dataset.timePercent);tick.style.top=`${top}%`;const pixel=height*top/100;tick.hidden=pixel-lastTick<32;if(!tick.hidden)lastTick=pixel;});
      const tiles=[...document.querySelectorAll('.photo-grid [data-select-file]')];
      const visible=tiles.find(el=>el.getBoundingClientRect().top>=140) || tiles.find(el=>el.getBoundingClientRect().bottom>140);
      if(visible && !state.photoJumpAnchor){const index=state.photoItems.findIndex(item=>item.id===visible.dataset.selectFile);if(index>=0)state.photoPosition=(state.photoStart||0)+index;}
      const item=state.photoItems[(state.photoPosition||0)-(state.photoStart||0)];
      update(track,(state.photoPosition||0)/Math.max(1,(state.photoDateSummary?.total||1)-1),state.photoJumpAnchor?null:item?.captureTime?.slice(0,7)||'unknown');
    }, cancel
  };
  const tray=document.querySelector('#upload-tray');
  let trayFrame=0;
  if(tray)new MutationObserver(()=>{if(!trayFrame)trayFrame=requestAnimationFrame(()=>{trayFrame=0;controller.sync();})}).observe(tray,{attributes:true,childList:true});
  return controller;
}
