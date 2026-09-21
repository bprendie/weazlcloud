import {files, filePath, filesInFolder, folderLabel, folderName, takeouts, state, selectedName, liveCapsules, fileMatches, matchQuery, escapeHTML as esc} from './data.js';

const $ = s => document.querySelector(s);
const head = (label, title, description) => `<div class="page-head"><span class="eyebrow">${label}</span><h1>${title}</h1><p>${description}</p></div>`;
const kindClass = kind => `type-${String(kind || 'file').toLowerCase().replace(/[^a-z0-9]+/g, '-')}`;

function qrMarkup(token) {
  const cells = [];
  for (let i = 0; i < 21 * 21; i++) {
    const on = (token.charCodeAt(i % token.length) + i * 7) % 5 < 2 || i % 21 < 2 || i % 21 > 18 || i < 21 * 2 || i > 21 * 19;
    cells.push(`<i class="${on ? 'on' : ''}"></i>`);
  }
  return `<div class="qr" aria-hidden="true">${cells.join('')}</div>`;
}

function buildTree(list) {
  const root = {name: '', path: '', folders: {}, files: []};
  for (const file of list) {
    let node = root, path = '';
    for (const part of file.folders) {
      path = path ? `${path}/${part}` : part;
      if (!node.folders[part]) node.folders[part] = {name: part, path, folders: {}, files: []};
      node = node.folders[part];
    }
    if (file.folder) {
      if (!node.folders[file.title]) node.folders[file.title] = {name: file.title, path: file.path, folders: {}, files: []};
    } else node.files.push(file);
  }
  return root;
}

function countNode(node) {
  return node.files.length + Object.values(node.folders).reduce((n, c) => n + countNode(c), 0);
}

function isSelected(type, key) {
  if (!state.selected) return false;
  return type === 'file' ? state.selected.type === 'file' && state.selected.id === key : state.selected.type === 'folder' && state.selected.path === key;
}

function treeRows(node, depth) {
  let html = '';
  for (const name of Object.keys(node.folders)) {
    const child = node.folders[name];
    const open = state.expanded.includes(child.path);
    const n = countNode(child);
    const on = isSelected('folder', child.path);
    html += `<div class="tree-row${on ? ' highlight' : ''}" style="padding-left:${12 + depth * 16}px" data-ctx-folder="${esc(child.path)}">
      <button class="tree-toggle" data-folder="${esc(child.path)}" aria-expanded="${open}"><span class="twisty">${open ? '−' : '+'}</span><span class="kind type-dir">DIR</span><strong>${esc(child.name)}</strong></button>
      <span class="ver-count">${n} ${n === 1 ? 'file' : 'files'}</span>
      <button class="icon-button menu-btn" data-menu-folder="${esc(child.path)}" aria-label="Folder actions">⋯</button>
    </div>`;
    if (open) {
      html += treeRows(child, depth + 1);
      html += child.files.map(f => {
        const hit = isSelected('file', f.id) ? ' highlight' : '';
        return `<div class="file-row tree-file${hit}" style="padding-left:${28 + (depth + 1) * 16}px" data-ctx-file="${f.id}" data-drag-file="${esc(f.folders.concat(f.title).join('/'))}" draggable="true">
          <button data-select-file="${f.id}" aria-label="Select ${esc(f.title)}"><span class="kind ${kindClass(f.kind)}">${esc(f.kind)}</span><span><strong>${esc(f.title)}</strong></span></button>
          <span class="size">${esc(f.size)}</span>
          <button class="icon-button menu-btn" data-menu-file="${f.id}" aria-label="File actions">⋯</button>
        </div>`;
      }).join('');
    }
  }
  return html;
}

function folderNode(node, path) {
  if (!path) return node;
  return path.split('/').reduce((current, part) => current?.folders?.[part], node) || {folders: {}, files: []};
}

function modifiedLabel(value) {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString([], {year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'});
}

function compareLibrary(a, b) {
  const direction = state.librarySortDir === 'desc' ? -1 : 1;
  let left, right;
  if (state.librarySort === 'modified') { left = a.mtime || ''; right = b.mtime || ''; }
  else if (state.librarySort === 'size') { left = a.bytes || 0; right = b.bytes || 0; }
  else if (state.librarySort === 'type') { left = a.kind || ''; right = b.kind || ''; }
  else { left = a.title || a.name || ''; right = b.title || b.name || ''; }
  return String(left).localeCompare(String(right), undefined, {numeric: true, sensitivity: 'base'}) * direction;
}

function fileRow(f, fullPath = false) {
  if (state.libraryView === 'grid') return gridFileCard(f, fullPath);
  const hit = isSelected('file', f.id) ? ' highlight' : '';
  const path = f.folders.concat(f.title).join('/');
  return `<div class="file-row tree-file${hit}" data-ctx-file="${f.id}" data-drag-file="${esc(path)}" draggable="true">
    <button data-select-file="${f.id}" aria-label="Select ${esc(f.title)}"><span class="kind ${kindClass(f.kind)}">${esc(f.kind)}</span><span class="file-name"><strong>${esc(f.title)}</strong>${fullPath ? `<small>${esc(path)}</small>` : ''}</span></button>
    <span class="library-modified">${esc(modifiedLabel(f.mtime))}</span><span class="size">${esc(f.size)}</span>
    <button class="icon-button menu-btn" data-menu-file="${f.id}" aria-label="File actions">⋯</button>
  </div>`;
}

function gridFileCard(f, fullPath = false) {
  const hit = isSelected('file', f.id) ? ' selected' : '';
  const path = f.folders.concat(f.title).join('/');
  const href = `/api/library?path=${encodeURIComponent(path)}&preview=1`;
  const kind = String(f.kind || '').toUpperCase();
  const ext = path.includes('.') ? path.slice(path.lastIndexOf('.') + 1).toLowerCase() : '';
  const preview = ['IMG', 'JPG', 'JPEG', 'PNG', 'GIF', 'WEB', 'WEBP', 'SVG', 'STL', '3MF'].includes(kind)
    ? `<img class="grid-preview" src="${href}" alt="" loading="lazy">`
    : ['MD', 'TXT', 'CSV', 'JSON', 'XML', 'LOG', 'DOC', 'DOCX', 'XLS', 'XLSX', 'PPT', 'PPTX', 'ODT', 'ODS', 'ODP'].includes(kind) || ['docx', 'xlsx', 'pptx', 'odt', 'ods', 'odp'].includes(ext)
    ? `<div class="grid-text-preview" data-grid-text-preview="${esc(path)}"><span>Loading preview…</span></div>`
    : `<div class="grid-kind"><span class="kind ${kindClass(f.kind)}">${esc(f.kind)}</span></div>`;
  return `<div class="library-card${hit}" data-ctx-file="${f.id}" data-drag-file="${esc(path)}" draggable="true">
    <button class="grid-open" data-select-file="${f.id}" aria-label="Open ${esc(f.title)}">${preview}</button>
    <div class="grid-card-info"><span class="kind ${kindClass(f.kind)}">${esc(f.kind)}</span><strong>${esc(f.title)}</strong>${fullPath ? `<small>${esc(path)}</small>` : ''}<span class="grid-meta">${esc(f.size)} · ${esc(modifiedLabel(f.mtime))}</span></div>
    <button class="icon-button menu-btn grid-menu" data-menu-file="${f.id}" aria-label="File actions">⋯</button>
  </div>`;
}

function folderRow(child) {
  const n = countNode(child);
  const on = isSelected('folder', child.path);
  if (state.libraryView === 'grid') return `<div class="library-card folder-card${on ? ' selected' : ''}" data-ctx-folder="${esc(child.path)}"><button class="grid-open" data-open-folder="${esc(child.path)}" aria-label="Open ${esc(child.name)}"><div class="grid-folder-preview"><span class="kind type-dir">DIR</span></div></button><div class="grid-card-info"><span class="kind type-dir">DIR</span><strong>${esc(child.name)}</strong><span class="grid-meta">${n} ${n === 1 ? 'item' : 'items'}</span></div><button class="icon-button menu-btn grid-menu" data-menu-folder="${esc(child.path)}" aria-label="Folder actions">⋯</button></div>`;
  return `<div class="tree-row${on ? ' highlight' : ''}" data-ctx-folder="${esc(child.path)}">
    <button class="tree-toggle" data-open-folder="${esc(child.path)}" aria-label="Open ${esc(child.name)}"><span class="kind type-dir">DIR</span><strong>${esc(child.name)}</strong></button>
    <span class="library-modified">${esc(modifiedLabel(child.mtime))}</span><span class="ver-count">${n} ${n === 1 ? 'item' : 'items'}</span>
    <button class="icon-button menu-btn" data-menu-folder="${esc(child.path)}" aria-label="Folder actions">⋯</button>
  </div>`;
}

function libraryRows(node) {
  let html = '';
  for (const child of Object.values(node.folders).sort(compareLibrary)) {
    html += folderRow(child);
  }
  html += [...node.files].sort(compareLibrary).map(f => fileRow(f)).join('');
  return html;
}

function librarySearchRows() {
  const query = state.librarySearch.trim().toLowerCase();
  const folders = new Map();
  for (const file of files) {
    let path = '';
    for (const part of file.folders) {
      path = path ? `${path}/${part}` : part;
      if (matchQuery(`${part} ${path}`, query)) folders.set(path, {title: part, name: part, path, folders: path.split('/').slice(0, -1), kind: 'DIR', size: 'folder', folder: true});
    }
  }
  const folderRows = [...folders.values()].sort(compareLibrary).map(folder => folderRow(folder)).join('');
  const fileRows = files.filter(f => !f.folder && fileMatches(f, query)).sort(compareLibrary).map(f => fileRow(f, true)).join('');
  return folderRows + fileRows;
}

function libraryHeader() {
  return `<div class="library-columns"><span>NAME</span><span>DATE MODIFIED</span><span>FILE SIZE</span></div>`;
}

function libraryBreadcrumb() {
  const parts = state.currentPath ? state.currentPath.split('/') : [];
  let path = '';
  const crumbs = [`<button class="crumb-button" data-library-path="">LIBRARY</button>`];
  for (const part of parts) {
    path = path ? `${path}/${part}` : part;
    crumbs.push(`<span class="crumb-sep">›</span><button class="crumb-button" data-library-path="${esc(path)}">${esc(part)}</button>`);
  }
  return `<nav class="library-breadcrumb" aria-label="Library location">${crumbs.join('')}</nav>`;
}

function home() {
  const live = liveCapsules();
  const grab = state.grabBase || 'grab base not set';
  const list = live.length
    ? `<div class="point-list">${live.slice(0, 3).map(c => `<button class="point-row" data-view="capsules"><span class="point-when">${esc(c.label)}</span><span class="point-scope">${esc(c.name)}</span><span class="point-dedupe">${esc(c.gate)}</span><span class="point-age">${esc(c.expiry)}</span><span class="tag">${esc(c.status)}</span></button>`).join('')}</div>`
    : '<p class="empty">Nothing minted yet. Put a file in the library, then send a grab link.</p>';
  return `<section class="hero"><div class="hero-copy"><p class="eyebrow">SIGNAL OVER NOISE</p><h1>Your files. Your node. Send a grab link.</h1><p>Library on this machine. recipient gets a URL. Files opens the same tree — no extra mount helper.</p><div class="hero-actions"><button class="primary" data-view="library">Open library</button><button class="secondary" data-view="send">Mint a grab link ↗</button></div></div><img src="weazlhead.png" alt="Weazl" width="255" height="251"></section>
    <section><div class="section-title"><h2>Live capsules</h2><button class="text-button" data-view="capsules">All capsules ↗</button></div>
    ${list}
    <p class="eyebrow" style="margin-top:22px">${live.length} LIVE · BURN-AFTER-READ IS THE DEFAULT · ${esc(grab)}</p></section>`;
}

function library() {
  const sel = selectedName();
  const node = folderNode(buildTree(files), state.currentPath);
  const parent = state.currentPath.split('/').slice(0, -1).join('/');
  const searching = state.librarySearch.trim().length > 0;
  const rows = searching ? librarySearchRows() : libraryRows(node);
  return head('WEAZLCLOUD / LIBRARY', 'A file is present or it is not.', 'Right-click a file (or ⋯) to send a grab link, download, or delete. Upload lands in the library.') +
    `<div class="library-workspace" data-ctx-tree="1">${libraryBreadcrumb()}
    <div class="library-controls"><label class="search library-search"><span>⌕</span><input type="search" data-library-search placeholder="Search the entire library" value="${esc(state.librarySearch)}"></label><label class="library-sort">Sort<select data-library-sort><option value="name" ${state.librarySort === 'name' ? 'selected' : ''}>Name</option><option value="modified" ${state.librarySort === 'modified' ? 'selected' : ''}>Date modified</option><option value="size" ${state.librarySort === 'size' ? 'selected' : ''}>File size</option><option value="type" ${state.librarySort === 'type' ? 'selected' : ''}>File type</option></select></label><button class="secondary sort-direction" data-library-sort-dir>${state.librarySortDir === 'asc' ? 'A → Z' : 'Z → A'}</button></div>
    <div class="hero-actions">
      <button class="primary" data-view="send" ${sel ? '' : 'disabled'}>Send ${sel ? esc(sel.split('/').pop()) : 'selection'} →</button>
      ${state.currentPath ? `<button class="secondary" data-library-path="${esc(parent)}">..</button>` : ''}
      <button class="secondary" data-action="upload">Upload…</button>
      <button class="secondary" data-action="upload-folder">Upload folder…</button>
      <button class="secondary" data-action="new-folder">New folder</button>
    </div>
    ${searching ? `<p class="library-result-count">Search results across the library</p>` : ''}
    <div class="view-toggle"><span>VIEW</span><button class="${state.libraryView === 'list' ? 'active' : ''}" data-library-view="list">☷ List</button><button class="${state.libraryView === 'grid' ? 'active' : ''}" data-library-view="grid">▦ Grid</button></div>
    <div class="${state.libraryView === 'grid' ? 'library-grid' : 'tree'}">${state.libraryView === 'list' ? libraryHeader() : ''}${rows || `<p class="empty">${searching ? 'No matching files.' : 'This folder is empty. Upload a weazldoc.'}</p>`}</div></div>`;
}

function send() {
  if (!state.selected) {
    return head('WEAZLCLOUD / SEND', 'Mint a grab link.', 'Pick a file or a folder in Library first. recipient never creates an account.') +
      `<p class="empty">Nothing selected.<br><button class="primary" data-view="library">Open library →</button></p>`;
  }
  const name = selectedName();
  const kind = state.selected.type === 'folder' ? 'folder' : 'file';
  const minted = state.minted;
  const result = minted ? `<div class="mint-result" id="mint-result">
      <div class="panel-top"><span>GRAB LINK</span><span>${esc(minted.gate).toUpperCase()}</span></div>
      <p class="mint-url">${esc(minted.url)}</p>
      ${state.engine ? `<img class="mint-qr" src="/api/qr?url=${encodeURIComponent(minted.url)}" alt="QR code for this grab link">` : qrMarkup(minted.id)}
      <p class="eyebrow">${state.engine ? 'SCAN TO OPEN THE GRAB LINK' : 'QR FIXTURE · PREVIEW ONLY'}</p>
      <div class="hero-actions">
        <button class="primary" data-action="copy-url">Copy URL</button>
        <a class="secondary button-link" href="${esc(minted.url && minted.url.startsWith('http') ? minted.url : 'grab.html?c=' + minted.id)}" target="_blank" rel="noopener">Open as recipient ↗</a>
      </div>
    </div>` : '';
  return head('WEAZLCLOUD / SEND', kind === 'folder' ? 'Send this folder.' : 'Send this file.', 'Sealed at mint. Later edits do not change recipient’s link. Read-only. Burn-after-read is the default.') +
    result +
    `<div class="panel active-place" style="margin-bottom:22px"><div class="panel-top"><span>${kind === 'folder' ? 'DIR' : 'FILE'}</span><span>SELECTED</span></div><h3>${esc(name)}</h3><p>${kind === 'folder' ? `${filesInFolder(state.selected.path).length} files sealed together.` : 'One file. Bytes recipient can open.'}</p></div>` +
    `<form id="send-form" class="policy">
      <fieldset><legend>How recipient gets in</legend>
        <label class="choice"><input type="radio" name="gate" value="open" ${state.gate === 'open' ? 'checked' : ''}><span><strong>Open</strong><small>URL is the capability. No extra phrase.</small></span></label>
        <label class="choice"><input type="radio" name="gate" value="passphrase" ${state.gate === 'passphrase' ? 'checked' : ''}><span><strong>Passphrase</strong><small>URL in one channel. Phrase in another.</small></span></label>
      </fieldset>
      ${state.gate === 'passphrase' ? `<label class="field">Passphrase for recipient<input name="phrase" type="password" value="${esc(state.passphrase)}" maxlength="1024" autocomplete="off"></label>` : ''}
      <fieldset><legend>What recipient can do</legend>
        <label class="choice"><input type="radio" name="mode" value="readonly" checked><span><strong>Read-only</strong><small>Grab the sealed copy. Cannot write back.</small></span></label>
      </fieldset>
      <div class="policy-row">
        <label class="field">Label<input name="label" value="${esc(state.label)}" maxlength="40"></label>
        <label class="field">Expiry
          <select name="expiry">
            <option value="24h" ${state.expiry === '24h' ? 'selected' : ''}>24 hours</option>
            <option value="3d" ${state.expiry === '3d' ? 'selected' : ''}>3 days</option>
            <option value="7d" ${state.expiry === '7d' ? 'selected' : ''}>7 days</option>
          </select>
        </label>
        <label class="field">Grabs
          <select name="grabs">
            <option value="1" ${state.grabs === '1' ? 'selected' : ''}>1 then burn</option>
            <option value="5" ${state.grabs === '5' ? 'selected' : ''}>5</option>
            <option value="20" ${state.grabs === '20' ? 'selected' : ''}>20</option>
          </select>
        </label>
      </div>
      <button type="button" class="primary" data-action="mint">Mint grab link →</button>
    </form>`;
}

function capsules() {
  if (!state.capsules.length) return head('WEAZLCLOUD / CAPSULES', 'Nothing minted.', 'Send a file or a folder first.') + '<p class="empty">No capsules yet.</p>';
  return head('WEAZLCLOUD / CAPSULES', 'What you already minted.', 'recipient is a label you typed, not a login. Revoke bricks the URL.') +
    `<div class="file-list">${state.capsules.map(c => `<div class="file-row" data-ctx-capsule="${esc(c.id)}">
      <button data-open-grab="${esc(c.id)}" ${c.status !== 'live' ? 'disabled' : ''}>
        <span class="kind">${c.kind === 'folder' ? 'DIR' : 'FILE'}</span>
        <span><strong>${esc(c.label)}</strong><small>${esc(c.name)} · ${esc(c.gate)} · ${esc(c.expiry)}</small></span>
      </button>
      <span class="ver-count">${c.status === 'live' ? `${c.left} left` : 'burned'}</span>
      <button class="icon-button menu-btn" data-menu-capsule="${esc(c.id)}" aria-label="Capsule actions">⋯</button>
    </div>`).join('')}</div>`;
}

function places() {
  const dav = state.driveBase || 'davs://drive.your.domain';
  const token = state.driveToken
    ? (state.tokenShown ? state.driveToken : '···· ···· ····')
    : (state.engine ? 'Unlock the vault to see the Files password.' : 'wzcv-····-····-····');
  return head('WEAZLCLOUD / PLACES', 'Where can a phone reach this node?', 'Traefik already terminates HTTPS. The administrator owns the grab hostname; your Files mount address stays here.') +
    `<form id="places-form" class="auth-form">
      <label>Grab hostname <input value="${esc(state.grabBase.replace(/^https:\/\//, ''))}" readonly></label><p class="eyebrow">Set by the node administrator.</p>
      <label>Drive base (davs://)<input name="drive" value="${esc(state.driveBase)}" placeholder="davs://drive.your.domain" maxlength="200"></label>
      <button class="primary">Save places</button>
    </form>
    <div class="panel-grid" style="margin-top:28px">
      <article class="panel active-place"><div class="panel-top"><span>01 / GRAB</span><span>RECIPIENT</span></div><h3>Grab URL</h3><p>Mint refuses to fire until this is an https:// name. Not 127.0.0.1.</p></article>
      <article class="panel"><div class="panel-top"><span>02 / DRIVE</span><span>FILES</span></div><h3>Connect to Server</h3><p>No FUSE. Files → Other Locations → ${esc(dav)}</p></article>
    </div>
    <div class="panel" style="margin-top:13px">
      <div class="panel-top"><span>DRIVE TOKEN</span>${state.driveToken ? `<button type="button" class="text-button" data-action="toggle-token">${state.tokenShown ? 'Hide' : 'Reveal'}</button>` : ''}</div>
      <h3>${esc(token)}</h3>
      <p>Username <em>weazl</em>. This is not the vault passphrase.</p>
      ${state.engine ? '' : '<div class="hero-actions"><button class="secondary" data-action="rotate-token">Rotate token</button></div>'}
    </div>
    <p class="eyebrow" style="margin-top:22px">${state.engine ? 'TRAEFIK ALREADY TERMINATES HTTPS.' : 'PREVIEW ONLY. TRAEFIK IS ALREADY ON THE NETWORK.'}</p>`;
}

function kit() {
  return head('WEAZLCLOUD / RECOVERY KIT', 'A brick without the passphrase.', 'The .wzck envelope holds vault wrap, restic access, Places, and the drive token. Not the library. Not the phrase. Keep two copies offline.') +
    `<div class="hero-actions"><button class="primary" data-action="kit">Cut recovery kit →</button>${state.engine ? '' : '<button class="secondary" data-action="verify-kit">Verify checksums</button>'}</div>
    <p class="empty">${state.engine ? 'Writes weazlcloud-recovery.wzck on the data volume, then verifies it.<br>Copy that file to two USBs you will put in a drawer.' : 'Choose a writable folder on a USB drive you will put in a drawer.<br>Forget the phrase and this file is still a brick. That is the point.'}</p>`;
}

function takeout() {
  if (state.engine) {
    return head('WEAZLCLOUD / TAKEOUT', 'Bring the dump. Do not connect the cloud.', 'Google Takeout, iCloud export, OneDrive zip. Stretch. Not in this build.') +
      '<p class="empty">The dump comes here later. Weazl does not OAuth to those clouds.</p>';
  }
  const t = takeouts.find(x => x.id === state.takeout) || takeouts[0];
  return head('WEAZLCLOUD / TAKEOUT', 'Bring the dump. Do not connect the cloud.', 'Google Takeout, iCloud export, OneDrive zip. You already downloaded it. Weazl walks the tree. Restic keeps one copy of the repeats. No OAuth.') +
    `<div class="panel-grid">${takeouts.map(p => `<button class="panel ${p.id === t.id ? 'active-place' : ''}" data-takeout="${p.id}">
      <div class="panel-top"><span>${esc(p.name.toUpperCase())}</span><span>${p.id === t.id ? '●' : '○'}</span></div>
      <h3>${esc(p.name)}</h3><p>${esc(p.detail)}</p>
    </button>`).join('')}</div>
    <div class="stat-card" style="margin-top:22px">
      <div class="signal-heading">THIS DUMP</div>
      <p><em>${esc(t.unique)}</em> kept</p>
      <span class="eyebrow">${esc(t.files)} FILES · ${esc(t.apparent)} APPARENT · ${esc(t.note)}</span>
    </div>
    <div class="hero-actions"><button class="primary" data-action="ingest">Ingest takeout →</button></div>
    <p class="eyebrow" style="margin-top:22px">STRETCH GOAL. THE DUMP COMES HERE. WEAZL DOES NOT GO THERE.</p>`;
}

function check() {
  return head('THE FINE PRINT / CHECK', 'Is the store still the store?', 'A cheap health read. Filenames stay off this screen.') +
    `<div class="hero-actions"><button class="primary" data-action="check">Check store →</button></div>`;
}

function destroy() {
  const live = liveCapsules();
  if (!live.length) {
    return head('THE FINE PRINT / DESTROY', 'Brick a capsule.', 'Nothing live to brick.') +
      '<p class="empty">No live capsules. Mint one, or revoke from Capsules.</p>';
  }
  if (state.engine) {
    return head('THE FINE PRINT / DESTROY', 'Brick a capsule.', 'Revoke destroys unwrap material. The URL becomes a brick.') +
      `<div class="file-list">${live.map(c => `<div class="file-row"><span class="kind">${c.kind === 'folder' ? 'DIR' : 'FILE'}</span><span><strong>${esc(c.label)}</strong><small>${esc(c.name)}</small></span><button class="text-button" data-revoke="${esc(c.id)}">Revoke</button></div>`).join('')}</div>`;
  }
  return head('THE FINE PRINT / DESTROY', 'Brick a capsule.', `Type DESTROY ${esc(state.destroyId)} to revoke that grab for good. The sealed bytes go with it.`) +
    `<form id="destroy-form" class="auth-form"><label>Confirmation phrase<input name="phrase" required autocomplete="off" placeholder="DESTROY ${esc(state.destroyId)}"></label><button class="primary">Destroy capsule</button></form>`;
}

function admin() {
  const pending = state.requests.filter(q => q.status === 'pending');
  return head('NODE ADMIN / ACCESS', 'Approve local accounts.', 'Identity approval only. User vaults and their contents stay outside the administrator view.') +
    `<div class="panel" style="margin-bottom:22px"><div class="panel-top"><span>NODE ADDRESS</span><span>ADMIN ONLY</span></div><h3>Grab hostname</h3><p>Users can mint links against this hostname but cannot change it.</p><form id="node-settings-form" class="inline-form"><input name="hostname" value="${esc(state.nodeHostname)}" placeholder="grab.your.domain" maxlength="200" required><button class="primary">Save hostname</button></form></div>` +
    `<div class="file-list admin-requests">${state.requests.length ? state.requests.map(q => `<div class="file-row"><span class="kind ${q.status === 'pending' ? 'type-dir' : ''}">${esc(q.status.toUpperCase())}</span><span><strong>${esc(q.username)}</strong><small>${esc(q.note || 'No note')} · ${esc(new Date(q.created_at).toLocaleString())}</small></span>${q.status === 'pending' ? `<button class="text-button" data-admin-approve="${esc(q.id)}">Approve</button><button class="text-button" data-admin-reject="${esc(q.id)}">Reject</button>` : ''}</div>`).join('') : '<p class="empty">No account requests.</p>'}</div>`;
}

export function renderMain() {
  const pages = {home, library, send, capsules, places, admin, kit, takeout, check, destroy};
  const html = (pages[state.view] || home)();
  $('#content').innerHTML = html;
  const crumb = {home: 'HOME', library: 'LIBRARY', send: 'SEND', capsules: 'CAPSULES', places: 'PLACES', admin: 'ADMIN', kit: 'KIT', takeout: 'TAKEOUT', check: 'CHECK', destroy: 'DESTROY'};
  $('#breadcrumb').textContent = state.view === 'library' && state.currentPath
    ? `LIBRARY / ${state.currentPath.split('/').join(' / ')}`
    : (crumb[state.view] || state.view.toUpperCase());
  document.querySelectorAll('nav [data-view]').forEach(b => {
    const active = b.dataset.view === state.view;
    b.classList.toggle('active', active);
    if (active) b.setAttribute('aria-current', 'page');
    else b.removeAttribute('aria-current');
  });
  renderSide();
}

export function renderSide() {
  const sendView = state.view === 'send';
  const picking = (state.view === 'library' || sendView) && state.selected;
  $('#side-title').innerHTML = picking || sendView ? 'This grab' : 'This node';
  $('#side-eyebrow').textContent = sendView ? 'SEALED AT MINT.' : picking ? 'THEN SEND.' : 'WHAT HAPPENS NEXT.';
  $('#side-action').textContent = (sendView || state.view === 'library') ? 'Clear' : 'Places';
  if (state.selected) {
    const name = selectedName();
    const kind = state.selected.type === 'folder' ? 'DIR' : 'FILE';
    $('#queue').innerHTML = `<div class="side-row"><span class="kind">${kind}</span><span><strong>${esc(name.split('/').pop())}</strong><small>${esc(name)}</small></span></div>`;
    return;
  }
  const grabHost = state.grabBase ? state.grabBase.replace(/^https:\/\//, '') : 'not set';
  const davHost = state.driveBase ? state.driveBase.replace(/^davs:\/\//, '') : 'not set';
  $('#queue').innerHTML = `<div class="side-row"><span class="kind">GRAB</span><span><strong>${esc(grabHost)}</strong><small>phone reachability</small></span></div>
    <div class="side-row"><span class="kind">DAV</span><span><strong>${esc(davHost)}</strong><small>Files · no FUSE</small></span></div>
    <div class="side-row"><span class="kind">LIVE</span><span><strong>${liveCapsules().length} capsules</strong><small>burn-after-read default</small></span></div>`;
}

export function renderDeck() {
  const busy = state.operation !== 'idle';
  const ingest = state.operation === 'ingest';
  const minting = state.operation === 'mint';
  const dedupe = state.quota?.dedupe_percent ?? Math.round(state.dedupe);
  $('#now-title').textContent = ingest ? 'Ingesting takeout' : minting ? 'Sealing capsule' : 'Library ready';
  $('#now-artist').textContent = ingest ? `${takeouts.find(t => t.id === state.takeout)?.name || 'Takeout'} · preview` : minting ? `${selectedName()} · preview` : `${files.length} files · ${liveCapsules().length} live grabs`;
  $('#lane-b-l').textContent = 'DEDUPE';
  $('#lane-b').style.width = `${busy ? state.lanes.b : dedupe}%`;
  $('#lane-b-n').textContent = `${busy ? Math.round(state.lanes.b) : dedupe}%`;
  const quota = state.quota;
  const quotaPercent = quota ? Math.round(quota.percent) : 0;
  const quotaBar = $('#quota-bar');
  if (quotaBar) quotaBar.style.width = `${quotaPercent}%`;
  const quotaNumber = $('#quota-n');
  if (quotaNumber) quotaNumber.textContent = quota ? `${quotaPercent}%` : '—';
  const quotaCaption = $('#quota-caption');
  if (quotaCaption) quotaCaption.textContent = quota ? `${formatBytes(quota.used)} used · ${formatBytes(quota.capacity)} volume` : 'Waiting for vault';
  const unit = $('#dedupe-unit');
  const heading = $('#dedupe-heading');
  if (state.engine && !busy) {
    $('#dedupe-n').textContent = String(files.length);
    if (unit) unit.textContent = '';
    if (heading) heading.textContent = 'IN THE LIBRARY';
    $('#dedupe-tag').textContent = 'LIBRARY';
    $('#dedupe-caption').textContent = 'FILES ON THIS NODE';
  } else {
    $('#dedupe-n').textContent = String(dedupe);
    if (unit) unit.textContent = '%';
    if (heading) heading.textContent = 'ALREADY IN THE STORE';
    $('#dedupe-tag').textContent = ingest ? 'INGEST' : minting ? 'MINT' : `DEDUPE ${dedupe}%`;
    $('#dedupe-caption').textContent = ingest ? 'NOT STORED TWICE · THIS DUMP' : 'NOT STORED TWICE · RESTIC';
  }
  $('#source').textContent = state.unlocked ? (busy ? (ingest ? 'INGESTING' : 'MINTING') : 'VAULT OPEN') : 'LOCKED';
  $('#strip-status').textContent = $('#source').textContent;
  $('#cancel').hidden = !busy;
  $('#send-now').disabled = busy || !state.unlocked;
  $('#send-now').hidden = busy;
  $('#username').innerHTML = state.unlocked ? 'vault<small>Open · this session owns the key</small>' : 'vault<small>Locked</small>';
  renderUploadTray();
}

export function renderUploadTray() {
  const tray = $('#upload-tray');
  const upload = state.upload;
  if (!tray || !upload || (!upload.active && !upload.total) || upload.dismissed) { if (tray) tray.hidden = true; return; }
  tray.hidden = false;
  const failed = upload.items?.filter(item => item.status === 'failed') || [];
  const title = upload.active ? `Uploading · ${upload.done} of ${upload.total} complete` : `${upload.done} of ${upload.total} uploaded`;
  const rails = (upload.rails || []).map((rail, index) => `<div class="upload-rail"><div><strong>RAIL ${index + 1}</strong><span class="upload-rail-name">${esc(rail.name || 'Waiting…')}</span></div><progress max="100" value="${Math.min(100, rail.pct || 0)}"></progress><small>${esc(rail.status || 'waiting')}</small></div>`).join('');
  const errors = failed.length ? `<div class="upload-errors">${failed.slice(0, 4).map(item => `<div><strong>${esc(item.target)}</strong><span>${esc(item.error || 'Upload failed')}</span></div>`).join('')}${failed.length > 4 ? `<small>…and ${failed.length - 4} more</small>` : ''}</div>` : '';
  const controls = `<div class="upload-controls">${failed.length ? '<button class="text-button" data-upload-retry>Retry failed</button>' : ''}${upload.active ? '<button class="text-button" data-upload-cancel>Cancel</button>' : '<button class="text-button" data-dismiss-upload>Dismiss</button>'}<button class="text-button" data-upload-collapse>${upload.collapsed ? 'Show details' : 'Collapse'}</button></div>`;
  tray.innerHTML = `<div class="upload-tray-head"><div><span class="eyebrow purple">LIBRARY / UPLOAD · 3 RAILS</span><strong>${title}</strong></div>${controls}</div>${upload.collapsed ? '' : `<div class="upload-rails">${rails}</div>${errors}`}<progress max="100" value="${Math.min(100, upload.percent || 0)}"></progress><p class="eyebrow">${Math.round(upload.percent || 0)}% of ${formatBytes(upload.totalBytes || 0)} · ${failed.length ? `${failed.length} failed` : upload.active ? 'working in the background' : 'ready'}</p>`;
}

function formatBytes(value) {
  const bytes = Number(value) || 0;
  if (bytes >= 1099511627776) return `${(bytes / 1099511627776).toFixed(1)} TB`;
  if (bytes >= 1073741824) return `${(bytes / 1073741824).toFixed(1)} GB`;
  if (bytes >= 1048576) return `${Math.round(bytes / 1048576)} MB`;
  if (bytes >= 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${bytes} B`;
}
