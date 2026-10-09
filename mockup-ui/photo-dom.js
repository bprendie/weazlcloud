// Keep decoded images and in-flight image consumers mounted during live updates.
// Scope changes (especially owner/Hidden) deliberately start a fresh projection.
export const photoTileIdentity = item => JSON.stringify([item.id,item.previewIdentity || item.revision || 0,item.userRotation || 0]);

export function renderPhotoContent(content, html, scope) {
  const template=document.createElement('template');template.innerHTML=html;
  const oldGrid=content.querySelector(':scope > .photo-grid');
  // DocumentFragment has no element for :scope to match in browsers.
  const nextGrid=[...template.content.children].find(node=>node.classList.contains('photo-grid'));
  if(content.photoScope!==scope || !oldGrid || !nextGrid){
    content.replaceChildren(template.content);content.photoScope=scope;return;
  }
  oldGrid.className=nextGrid.className;
  oldGrid.dataset.windowKey='';
  const remaining=new Set(content.children);
  let cursor=content.firstElementChild;
  for(const candidate of [...template.content.children]){
    let node=candidate===nextGrid?oldGrid:[...remaining].find(old=>old!==oldGrid && old.outerHTML===candidate.outerHTML);
    // A status update must not close an open maintenance menu.
    if(!node && candidate.classList.contains('photo-heading')){
      const old=content.querySelector('.photo-tools');
      if(old?.open)candidate.querySelector('.photo-tools')?.setAttribute('open','');
    }
    node ||= candidate;
    remaining.delete(node);
    if(node!==cursor)content.insertBefore(node,cursor);
    cursor=node.nextElementSibling;
  }
  for(const node of remaining)node.remove();
}

function updateTile(old, fresh) {
  old.className=fresh.className;old.dataset.dragFile=fresh.dataset.dragFile;
  const selector=old.querySelector('[data-photo-select]'), nextSelector=fresh.querySelector('[data-photo-select]');
  for(const attr of ['aria-label','aria-pressed','title'])selector.setAttribute(attr,nextSelector.getAttribute(attr));
  if(selector.textContent!==nextSelector.textContent)selector.textContent=nextSelector.textContent;
  const button=old.querySelector('.grid-open'), nextButton=fresh.querySelector('.grid-open');
  button.setAttribute('aria-label',nextButton.getAttribute('aria-label'));
  const img=button.querySelector('[data-photo-thumbnail]'), nextImg=nextButton.querySelector('[data-photo-thumbnail]');
  for(const attr of ['width','height'])img.setAttribute(attr,nextImg.getAttribute(attr));
  for(const badge of [...button.children])if(badge!==img)badge.remove();
  for(const badge of [...nextButton.children])if(badge!==nextImg)button.append(badge);
  const title=old.querySelector('.grid-card-info strong'), nextTitle=fresh.querySelector('.grid-card-info strong');
  if(title.textContent!==nextTitle.textContent)title.textContent=nextTitle.textContent;
  title.title=nextTitle.title;
}

export function reconcilePhotoRows(grid, rows, markup) {
  const oldRows=new Map([...grid.querySelectorAll(':scope > [data-photo-row-key]')].map(node=>[node.dataset.photoRowKey,node]));
  const oldTiles=new Map([...grid.querySelectorAll('[data-photo-preview-key]')].map(node=>[node.dataset.photoPreviewKey,node]));
  const top=grid.querySelector('[data-photo-spacer="top"]'), bottom=grid.querySelector('[data-photo-spacer="bottom"]');
  let cursor=top.nextElementSibling;
  const kept=new Set();
  for(const row of rows){
    const template=document.createElement('template');template.innerHTML=markup(row);
    const fresh=template.content.firstElementChild;
    let node=oldRows.get(row.key);
    if(!node)node=fresh;
    if(row.tiles.length){
      const cards=[...fresh.children].map((card,index)=>{
        const old=oldTiles.get(card.dataset.photoPreviewKey);
        if(old?.querySelector('[data-photo-thumbnail]')){updateTile(old,card);card=old;}
        card.style.width=`${row.tiles[index].width}px`;
        return card;
      });
      let child=node.firstElementChild;
      for(const card of cards){if(card!==child)node.insertBefore(card,child);child=card.nextElementSibling;}
      for(const stale of [...node.children])if(!cards.includes(stale))stale.remove();
    } else if(node.innerHTML!==fresh.innerHTML)node.innerHTML=fresh.innerHTML;
    node.style.height=`${row.height}px`;
    if(node!==cursor)grid.insertBefore(node,cursor || bottom);
    cursor=node.nextElementSibling;kept.add(node);
  }
  for(const node of oldRows.values())if(!kept.has(node))node.remove();
}
