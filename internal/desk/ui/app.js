import {files, filesInFolder, takeouts, state, selectedName, seedPreview, escapeHTML as esc} from './data.js';
import {renderMain, renderSide, renderDeck} from './views.js';
import * as engine from './engine.js';

const $ = s => document.querySelector(s);
let noticeTimer, frame, live = false, uploadPrefix = '';

function libraryPath() {
  if (!state.selected) return '';
  if (state.selected.type === 'folder') return state.selected.path;
  const f = files.find(x => x.id === state.selected.id);
  if (!f) return '';
  return f.folders.concat(f.title).join('/');
}

function filePath(id) {
  const f = files.find(x => x.id === id);
  return f ? f.folders.concat(f.title).filter(Boolean).join('/') : id;
}

function hideMenu() {
  const el = $('#ctx');
  if (el) el.hidden = true;
}

function showMenu(x, y, items) {
  const el = $('#ctx');
  if (!el) return;
  el.innerHTML = items.map(item => item.sep
    ? '<div class="sep"></div>'
    : `<button type="button" role="menuitem" data-act="${item.act}">${item.label}</button>`).join('');
  el.hidden = false;
  const pad = 8;
  const w = el.offsetWidth, h = el.offsetHeight;
  el.style.left = `${Math.min(x, innerWidth - w - pad)}px`;
  el.style.top = `${Math.min(y, innerHeight - h - pad)}px`;
}

function fileMenu(id) {
  return [
    {act: `send-file:${id}`, label: 'Share this'},
    {act: `preview-file:${id}`, label: 'Open preview'},
    {act: `download:${id}`, label: 'Download'},
    {act: `rename-file:${id}`, label: 'Rename'},
    {sep: true},
    {act: `delete-file:${id}`, label: 'Delete'}
  ];
}

function folderMenu(path) {
  return [
    {act: `send-folder:${path}`, label: 'Share this'},
    {act: `upload-here:${path}`, label: 'Upload into'},
    {act: `rename-folder:${path}`, label: 'Rename'},
    {sep: true},
    {act: `delete-folder:${path}`, label: 'Delete'}
  ];
}

function rootMenu() {
  return [
    {act: 'upload', label: 'Upload files…'},
    {act: 'upload-folder-root', label: 'Upload folder…'},
    {act: 'new-folder-root', label: 'New folder'}
  ];
}

async function moveFile(id, folder) {
  const from = filePath(id);
  const name = from.split('/').pop();
  const to = folder ? `${folder}/${name}` : name;
  if (from === to) return;
  if (!live) { toast('Files can move on the connected node.'); return; }
  try {
    await engine.renameLibrary(from, to);
    await loadLibrary();
    renderMain();
    toast(`Moved ${name} to ${folder || 'the library root'}.`);
  } catch (err) { toast(err.message); }
}

async function previewFile(id) {
  const f = files.find(x => x.id === id); if (!f || f.folder) return;
  const path = filePath(id);
  if (!live) { toast('Preview is available when the node is connected.'); return; }
  const previewable = new Set(['IMG', 'JPG', 'JPEG', 'PNG', 'GIF', 'WEB', 'WEBP', 'PDF', 'TXT', 'MD', 'CSV', 'JSON', 'MP3', 'WAV', 'FLA', 'MP4', 'MOV', 'WEBM']);
  if (!previewable.has(String(f.kind).toUpperCase())) { toast('This file opens as a download.'); return; }
  try {
    const result = await engine.previewLibrary(path);
    const url = URL.createObjectURL(result.blob);
    const type = result.type.split(';')[0];
    let body;
    if (type.startsWith('image/')) body = `<img class="file-preview-image" src="${url}" alt="${esc(f.title)}">`;
    else if (type.startsWith('text/')) body = `<pre class="file-preview-text">${esc(await result.blob.text())}</pre>`;
    else if (type === 'application/pdf' || type.startsWith('audio/') || type.startsWith('video/')) body = `<iframe class="file-preview-frame" src="${url}" title="${esc(f.title)}"></iframe>`;
    else { URL.revokeObjectURL(url); toast('This file opens as a download.'); return; }
    modal(`<span class="eyebrow purple">PREVIEW / ${esc(type)}</span><h2>${esc(f.title)}</h2>${body}<p class="eyebrow">${esc(path)}</p>`);
    $('#modal').addEventListener('close', () => URL.revokeObjectURL(url), {once: true});
  } catch (err) { toast(err.message); }
}

function beginUpload(list, prefix = '') {
  const filesToUpload = [...list];
  if (!filesToUpload.length) return;
  const totalBytes = filesToUpload.reduce((n, f) => n + f.size, 0);
  state.upload = {active: true, current: '', done: 0, total: filesToUpload.length, loaded: 0, totalBytes, failed: []};
  const rails = Array.from({length: Math.min(3, filesToUpload.length)}, (_, i) => `<div class="upload-rail" id="upload-rail-${i}"><div><strong>RAIL ${i + 1}</strong><span class="upload-rail-name">Waiting…</span></div><progress max="100" value="0"></progress><small>0%</small></div>`).join('');
  modal(`<span class="eyebrow purple">LIBRARY / UPLOAD</span><h2>Uploading files</h2><div class="upload-rails">${rails}</div><progress id="upload-progress-bar" max="100" value="0"></progress><p id="upload-progress-text" class="eyebrow">0 / ${filesToUpload.length} · parallel rails</p>`);
  (async () => {
    const loaded = new Array(filesToUpload.length).fill(0);
    const updateProgress = () => {
      const sent = loaded.reduce((n, value) => n + value, 0);
      const pct = totalBytes ? (sent / totalBytes) * 100 : 100;
      const bar = $('#upload-progress-bar'); if (bar) bar.value = pct;
      const text = $('#upload-progress-text'); if (text) text.textContent = `${state.upload.done} / ${filesToUpload.length} · ${Math.round(pct)}%`;
    };
    let next = 0;
    const worker = async slot => {
      while (true) {
        const index = next++;
        if (index >= filesToUpload.length) return;
        const file = filesToUpload[index];
        const relative = file.webkitRelativePath || file.name;
        const target = [prefix, relative].filter(Boolean).join('/');
        state.upload.current = target;
        const rail = $(`#upload-rail-${slot}`);
        const railName = rail?.querySelector('.upload-rail-name');
        const railProgress = rail?.querySelector('progress');
        const railPercent = rail?.querySelector('small');
        if (railName) railName.textContent = target;
        if (railProgress) railProgress.value = 0;
        if (railPercent) railPercent.textContent = '0%';
        try {
          await engine.putLibraryProgress(target, file, (sent) => {
            loaded[index] = sent;
            const pct = file.size ? (sent / file.size) * 100 : 100;
            if (railProgress) railProgress.value = pct;
            if (railPercent) railPercent.textContent = `${Math.round(pct)}%`;
            updateProgress();
          });
          loaded[index] = file.size;
          state.upload.done++;
          if (railName) railName.textContent = `✓ ${target}`;
          if (railProgress) railProgress.value = 100;
          if (railPercent) railPercent.textContent = 'done';
        } catch (err) {
          state.upload.failed.push(`${target}: ${err.message}`); loaded[index] = file.size;
          if (railName) railName.textContent = `× ${target}`;
          if (railPercent) railPercent.textContent = 'failed';
        }
        updateProgress();
      }
    };
    await Promise.all(Array.from({length: Math.min(3, filesToUpload.length)}, (_, slot) => worker(slot)));
    state.upload.active = false;
    await loadLibrary();
    renderMain(); renderDeck();
    const failures = state.upload.failed;
    $('#modal-content').innerHTML = `<span class="eyebrow purple">LIBRARY / UPLOAD COMPLETE</span><h2>${state.upload.done} of ${filesToUpload.length} uploaded</h2>${failures.length ? `<p class="warn">${failures.map(esc).join('<br>')}</p><button class="primary" data-close>Close</button>` : '<p>All files are in the library.</p><button class="primary" data-close>Done</button>'}`;
    toast(failures.length ? `${failures.length} upload${failures.length === 1 ? '' : 's'} failed.` : 'Upload complete.');
  })();
}

function capsuleMenu(id) {
  const cap = state.capsules.find(c => c.id === id);
  if (!cap || cap.status !== 'live') return [{act: `open-grab:${id}`, label: 'This grab is gone'}];
  return [
    {act: `copy-capsule:${id}`, label: 'Copy grab URL'},
    {act: `open-grab:${id}`, label: 'Open as Gil'},
    {sep: true},
    {act: `revoke:${id}`, label: 'Revoke'}
  ];
}

function toast(message) {
  clearTimeout(noticeTimer);
  $('#toast').textContent = message;
  $('#toast').hidden = false;
  noticeTimer = setTimeout(() => { $('#toast').hidden = true; }, 3200);
}

function modal(html) {
  $('#modal-content').innerHTML = html;
  $('#modal').showModal();
}

function help() {
  modal('<span class="eyebrow purple">KEEP YOUR HANDS ON THE KEYS</span><h2>The short route.</h2>' +
    [['Home / Library / Send / Capsules / Places', '1–5'], ['Mint grab link', 'S'], ['Lock vault', 'L'], ['File / folder actions', 'Right-click or ⋯'], ['This cheat sheet', '?']].map(([a, b]) => `<div class="shortcut"><span>${a}</span><kbd>${b}</kbd></div>`).join(''));
}

function vaultCard() {
  modal(`<img class="auth-brand" src="weazlcloud.png" alt="WeazlCloud"><span class="eyebrow purple">THIS SESSION</span><h2>Vault ${state.unlocked ? 'open' : 'locked'}</h2><p>Closing the tab detaches. Lock zeroes the key.${state.engine ? '' : ' Preview only — no real vault.'}</p><div class="dialog-actions"><button class="primary" id="do-lock">${state.unlocked ? 'Lock vault' : 'Unlock…'}</button></div>`);
}

function navigate(view) {
  state.view = view;
  renderMain();
}

function selectFile(id) {
  state.selected = {type: 'file', id};
  state.minted = null;
  renderMain();
  renderDeck();
}

function selectFolder(path) {
  state.selected = {type: 'folder', path};
  state.minted = null;
  renderMain();
  renderDeck();
}

function sendFile(id) {
  selectFile(id);
  navigate('send');
}

function sendFolder(path) {
  selectFolder(path);
  navigate('send');
}

function stopWork() {
  if (frame) cancelAnimationFrame(frame);
  frame = 0;
  state.operation = 'idle';
  state.percent = 0;
  state.lanes = {a: 0, b: state.dedupe, c: 0};
  renderDeck();
}

function tickMint(now) {
  if (document.visibilityState === 'hidden') { frame = requestAnimationFrame(tickMint); return; }
  const t = Math.min(1, (now - state.started) / 1600);
  state.lanes = {a: Math.min(100, t * 140), b: 41 + t * 20, c: t * 100};
  renderDeck();
  if (t < 1) { frame = requestAnimationFrame(tickMint); return; }
  finishMint();
}

function finishMint() {
  if (frame) cancelAnimationFrame(frame);
  frame = 0;
  const id = `c${Date.now().toString(36).slice(-6)}`;
  const name = selectedName();
  const kind = state.selected.type;
  const capsule = {
    id,
    label: state.label || 'Gil',
    name,
    kind,
    gate: state.gate,
    expiry: state.expiry === '24h' ? '24h left' : state.expiry === '3d' ? '3 days left' : '7 days left',
    left: Number(state.grabs),
    status: 'live'
  };
  state.capsules.unshift(capsule);
  state.minted = {id, url: `${state.grabBase.replace(/\/$/, '')}/g/${id}`, gate: state.gate, name};
  const members = kind === 'folder'
    ? filesInFolder(state.selected.path).map(f => ({title: f.title, size: f.size, kind: f.kind}))
    : [{title: name, size: files.find(f => f.id === state.selected.id)?.size || '', kind: files.find(f => f.id === state.selected.id)?.kind || 'FILE'}];
  const bag = JSON.parse(localStorage.getItem('wzcl-capsules') || '{}');
  bag[id] = {token: id, name, kind, size: kind === 'folder' ? `${members.length} files` : members[0].size, gate: state.gate, phrase: state.passphrase, status: 'live', expiry: capsule.expiry, files: members};
  localStorage.setItem('wzcl-capsules', JSON.stringify(bag));
  state.operation = 'idle';
  state.dedupe = Math.min(96, state.dedupe + 1);
  state.lanes = {a: 0, b: state.dedupe, c: 0};
  renderMain();
  renderDeck();
  $('#mint-result')?.scrollIntoView({block: 'nearest'});
  toast('Grab link minted in this preview. Nothing left this machine.');
}

async function mint() {
  if (!state.unlocked) { toast('Unlock the vault first.'); return; }
  if (!state.selected) { navigate('library'); toast('Pick a file or a folder first.'); return; }
  if (!state.grabBase.startsWith('https://')) { toast('Set an https:// grab base in Places.'); navigate('places'); return; }
  if (state.gate === 'passphrase' && !state.passphrase) { toast('Give Gil a passphrase, or switch to Open.'); return; }
  if (state.operation !== 'idle') return;
  if (live) {
    try {
      const row = await engine.mintCapsule({
        path: libraryPath(),
        kind: state.selected.type,
        gate: state.gate,
        passphrase: state.passphrase,
        label: state.label,
        expiry: state.expiry,
        grabs: Number(state.grabs)
      });
      await loadCapsules();
      state.minted = {id: row.id, url: row.url, gate: row.gate, name: row.name};
      renderMain();
      renderDeck();
      $('#mint-result')?.scrollIntoView({block: 'nearest'});
      toast('Grab link minted. Sealed copy. Gil cannot write back.');
    } catch (err) {
      toast(err.message);
    }
    return;
  }
  state.operation = 'mint';
  state.started = performance.now();
  renderDeck();
  frame = requestAnimationFrame(tickMint);
}

function tickIngest(now) {
  if (document.visibilityState === 'hidden') { frame = requestAnimationFrame(tickIngest); return; }
  const t = Math.min(1, (now - state.started) / 4200);
  state.lanes = {a: Math.min(100, t * 120), b: 20 + t * 70, c: t < 0.4 ? 0 : ((t - 0.4) / 0.6) * 100};
  state.dedupe = 20 + t * 55;
  renderDeck();
  if (t < 1) { frame = requestAnimationFrame(tickIngest); return; }
  const tko = takeouts.find(x => x.id === state.takeout);
  state.dedupe = 84;
  stopWork();
  toast(`${tko?.name || 'Takeout'} preview finished. ${tko?.unique} would be kept. No dump was read.`);
}

function ingest() {
  if (!state.unlocked) { toast('Unlock the vault first.'); return; }
  if (state.operation !== 'idle') return;
  state.operation = 'ingest';
  state.started = performance.now();
  renderDeck();
  frame = requestAnimationFrame(tickIngest);
  toast('Walking a fixture dump. The real engine is not connected.');
}

async function lockVault() {
  stopWork();
  if (live) {
    try { await engine.lock(); } catch (err) { toast(err.message); return; }
  }
  state.unlocked = false;
  $('.app').hidden = true;
  $('#unlock-screen').hidden = false;
  $('#unlock-form').reset();
  renderDeck();
  toast(live ? 'Vault locked. Key zeroed.' : 'Vault locked. Key zeroed in this preview.');
}

async function loadLibrary() {
  if (!live) return;
  const rows = await engine.listLibrary();
  files.splice(0, files.length, ...rows.map(engine.toFixture));
  const folders = [...new Set(files.map(f => f.folders.join('/')))];
  state.expanded = folders;
}

function openDesk() {
  state.unlocked = true;
  $('#unlock-screen').hidden = true;
  $('.app').hidden = false;
  renderMain();
  renderDeck();
}

async function revoke(id) {
  if (live) {
    try { await engine.revokeCapsule(id); await loadCapsules(); }
    catch (err) { toast(err.message); return; }
  } else {
    const cap = state.capsules.find(c => c.id === id);
    if (!cap) return;
    cap.status = 'burned';
    cap.left = 0;
    cap.expiry = 'revoked';
  }
  if (state.minted?.id === id) state.minted = null;
  renderMain();
  renderDeck();
  toast(live ? 'Capsule revoked. The URL is a brick.' : 'Capsule revoked in this preview. The URL would be a brick.');
}

async function refreshPlaces() {
  const places = await engine.loadPlaces().catch(() => null);
  if (!places) return;
  if (places.grab) state.grabBase = places.grab;
  else state.grabBase = '';
  if (places.drive) state.driveBase = places.drive;
  if (places.token) state.driveToken = places.token;
}

async function loadCapsules() {
  if (!live) return;
  const j = await engine.listCapsules();
  if (j.base) state.grabBase = j.base;
  state.capsules = (j.capsules || []).map(c => ({
    id: c.id,
    label: c.label,
    name: c.name,
    kind: c.kind,
    gate: c.gate,
    expiry: c.status === 'live' && c.expires ? `until ${new Date(c.expires).toLocaleString()}` : 'gone',
    left: c.left,
    status: c.status
  }));
}

function grabHref(id) {
  if (live) return `${state.grabBase.replace(/\/$/, '')}/g/${id}`;
  return `grab.html?c=${id}`;
}

async function runMenu(act) {
  hideMenu();
  const [kind, ...rest] = act.split(':');
  const key = rest.join(':');
  if (kind === 'send-file') sendFile(key);
  if (kind === 'send-folder') sendFolder(key);
  if (kind === 'preview-file') previewFile(key);
  if (kind === 'download') {
    const f = files.find(x => x.id === key);
    const path = filePath(key);
    if (live) {
      try { await engine.downloadLibrary(path, f?.title || key); }
      catch (err) { toast(err.message); }
    } else toast('Download is the engine. This preview has no bytes.');
  }
  if (kind === 'rename-file' || kind === 'rename-folder') {
    const from = kind === 'rename-file' ? filePath(key) : key;
    const next = prompt('Rename to', from.split('/').pop());
    if (!next || next.includes('/')) return;
    const to = from.split('/').slice(0, -1).concat(next).join('/');
    if (live) { try { await engine.renameLibrary(from, to); await loadLibrary(); renderMain(); toast('Renamed.'); } catch (err) { toast(err.message); } }
    else toast('Renamed in the connected node.');
  }
  if (kind === 'delete-file' || kind === 'delete-folder') {
    const f = files.find(x => x.id === key);
    const path = kind === 'delete-folder' ? key : (f ? f.folders.concat(f.title).join('/') : key);
    if (!confirm(`Delete ${path}? Present or not. No recycle bin.`)) return;
    if (live) {
      try {
        await engine.deleteLibrary(path);
        await loadLibrary();
        if (state.selected && (state.selected.id === key || state.selected.path === key)) state.selected = null;
        renderMain();
        renderDeck();
        toast('Gone from the current tree.');
      } catch (err) { toast(err.message); }
    } else toast('Delete is the engine. This preview does not drop files.');
  }
  if (kind === 'upload-here') { uploadPrefix = key; $('#upload-folder').click(); }
  if (kind === 'upload') { uploadPrefix = state.currentPath; $('#upload').click(); }
  if (kind === 'upload-folder-root') { uploadPrefix = state.currentPath; $('#upload-folder').click(); }
  if (kind === 'new-folder-root') {
    const name = prompt('New folder name');
    const base = state.currentPath ? `${state.currentPath}/` : '';
    if (name && live) engine.createFolder(base + name.trim()).then(async () => { await loadLibrary(); renderMain(); toast('Folder created.'); }).catch(err => toast(err.message));
  }
  if (kind === 'copy-capsule') {
    const url = grabHref(key);
    navigator.clipboard?.writeText(url).catch(() => {});
    toast('Grab URL copied.');
  }
  if (kind === 'open-grab') window.open(grabHref(key), '_blank', 'noopener');
  if (kind === 'revoke') revoke(key);
}

document.addEventListener('click', e => {
  const ctxBtn = e.target.closest('#ctx button');
  if (ctxBtn) { runMenu(ctxBtn.dataset.act); return; }
  const menuBtn = e.target.closest('[data-menu-file], [data-menu-folder], [data-menu-capsule]');
  if (menuBtn) {
    e.preventDefault();
    const r = menuBtn.getBoundingClientRect();
    if (menuBtn.dataset.menuFile) showMenu(r.left, r.bottom + 4, fileMenu(menuBtn.dataset.menuFile));
    else if (menuBtn.dataset.menuFolder) showMenu(r.left, r.bottom + 4, folderMenu(menuBtn.dataset.menuFolder));
    else showMenu(r.left, r.bottom + 4, capsuleMenu(menuBtn.dataset.menuCapsule));
    return;
  }
  hideMenu();
  const b = e.target.closest('button, a.button-link');
  if (!b) return;
  if (b.classList.contains('dialog-close') || b.dataset.close) { $('#modal').close(); return; }
  if (b.closest('form') && !b.dataset.action) return;
  if (b.dataset.view) navigate(b.dataset.view);
  if (b.dataset.openFolder) { state.currentPath = b.dataset.openFolder; state.selected = null; renderMain(); renderDeck(); }
  if (b.dataset.libraryPath !== undefined) { state.currentPath = b.dataset.libraryPath; state.selected = null; renderMain(); renderDeck(); }
  if (b.dataset.selectFile) { selectFile(b.dataset.selectFile); previewFile(b.dataset.selectFile); }
  if (b.dataset.selectFolder) selectFolder(b.dataset.selectFolder);
  if (b.dataset.sendFile) sendFile(b.dataset.sendFile);
  if (b.dataset.sendFolder) sendFolder(b.dataset.sendFolder);
  if (b.dataset.revoke) revoke(b.dataset.revoke);
  if (b.dataset.openGrab) {
    const cap = state.capsules.find(c => c.id === b.dataset.openGrab);
    if (cap?.status === 'live') window.open(grabHref(cap.id), '_blank', 'noopener');
  }
  if (b.dataset.takeout) { state.takeout = b.dataset.takeout; renderMain(); }
  if (b.dataset.action === 'mint') { e.preventDefault(); mint(); }
  if (b.dataset.action === 'copy-url') {
    navigator.clipboard?.writeText(state.minted?.url || '').catch(() => {});
    toast(live ? 'Grab URL copied.' : 'URL copied in this preview.');
  }
  if (b.dataset.action === 'upload') {
    if (live) { uploadPrefix = state.currentPath; $('#upload').click(); }
    else toast('Upload would land in the library. Preview only. Drive PUT does the same job.');
  }
  if (b.dataset.action === 'upload-folder') { if (live) { uploadPrefix = state.currentPath; $('#upload-folder').click(); } else toast('Folder upload is available on the connected node.'); }
  if (b.dataset.action === 'new-folder') {
    const name = prompt('New folder name');
    if (name && live) {
      const base = state.selected?.type === 'folder' ? `${state.selected.path}/` : (state.currentPath ? `${state.currentPath}/` : '');
      engine.createFolder(base + name.trim()).then(async () => { await loadLibrary(); renderMain(); toast('Folder created.'); }).catch(err => toast(err.message));
    }
  }
  if (b.dataset.action === 'kit') {
    if (live) {
      const phrase = prompt('Vault passphrase for the kit');
      if (phrase) engine.kit(phrase).then(() => toast('Recovery kit written on the node.')).catch(err => toast(err.message));
    } else toast('Would write weazlcloud-recovery.wzck. Passphrase stays in your head.');
  }
  if (b.dataset.action === 'verify-kit') toast('Checksums would run on the USB. Preview only.');
  if (b.dataset.action === 'check') {
    toast(live ? `${files.length} files in the current tree.` : 'Packs look healthy in this preview. Filenames stayed off the page.');
  }
  if (b.dataset.action === 'ingest') {
    if (live) toast('Takeout ingest is not in this build.');
    else ingest();
  }
  if (b.dataset.action === 'toggle-token') { state.tokenShown = !state.tokenShown; renderMain(); }
  if (b.dataset.action === 'rotate-token') {
    state.driveToken = `wzcv-${Math.random().toString(36).slice(2, 6)}-mock-token`;
    state.tokenShown = true;
    renderMain();
    toast('Drive token rotated in this preview. Old Files password would die.');
  }
  if (b.id === 'do-lock') { $('#modal').close(); if (state.unlocked) lockVault(); }
});

$('.brand').addEventListener('click', e => { e.preventDefault(); navigate('home'); });

document.addEventListener('change', e => {
  if (e.target.name === 'gate') {
    state.gate = e.target.value;
    renderMain();
  }
});

document.addEventListener('input', e => {
  if (e.target.name === 'phrase' && e.target.closest('#send-form')) state.passphrase = e.target.value;
  if (e.target.name === 'label') state.label = e.target.value;
  if (e.target.name === 'expiry') state.expiry = e.target.value;
  if (e.target.name === 'grabs') state.grabs = e.target.value;
});

$('#help').onclick = help;
$('#account').onclick = vaultCard;
$('#lock').onclick = lockVault;
$('#send-now').onclick = () => { state.view === 'send' ? mint() : (state.selected ? navigate('send') : navigate('library')); };
$('#cancel').onclick = () => { stopWork(); toast('Cancelled in this preview.'); };
$('#side-action').onclick = () => {
  if (state.view === 'send' || state.view === 'library') {
    state.selected = null;
    state.minted = null;
    renderMain();
    renderDeck();
    return;
  }
  navigate('places');
};

function setForgeMode(on) {
  state.forging = !!on;
  $('#confirm-row').hidden = !state.forging;
  const confirm = $('#unlock-form [name=confirm]');
  if (confirm) confirm.required = false;
  $('#unlock-description').textContent = state.forging
    ? 'Create the first local account. This node owns the identity and the vault.'
    : 'Log in to the local node. The passphrase never leaves this machine.';
  $('#forge-mode').textContent = state.forging ? 'Already set up? Log in' : 'New node? Create the first account';
  $('#unlock-form .primary').textContent = state.forging ? 'Create account →' : 'Log in →';
  $('#unlock-error').textContent = '';
}

$('#forge-mode').onclick = () => setForgeMode(!state.forging);

$('#unlock-form').onsubmit = async e => {
  e.preventDefault();
  const form = new FormData(e.target);
  const username = String(form.get('username') || '').trim();
  const pass = String(form.get('passphrase') || '');
  const confirm = String(form.get('confirm') || '');
  $('#unlock-error').textContent = '';
  if (!username) { $('#unlock-error').textContent = 'Username must not be empty.'; return; }
  if (!pass) { $('#unlock-error').textContent = 'Passphrase must not be empty.'; return; }
  if (state.forging && !confirm) { $('#unlock-error').textContent = 'Confirm the passphrase.'; return; }
  if (state.forging && pass !== confirm) { $('#unlock-error').textContent = 'Passphrases do not match.'; return; }
  if (live) {
    try {
      if (state.forging) {
        await engine.bootstrap(username, pass, pass, confirm);
      } else if (!state.authenticated) {
        await engine.login(username, pass);
        state.authenticated = true;
        $('#unlock-error').textContent = 'Logged in. Enter the passphrase again to unlock the vault.';
        $('#unlock-form .primary').textContent = 'Unlock vault →';
        return;
      } else {
        await engine.unlock(pass);
      }
    } catch (err) {
      const msg = err.message || 'unlock failed';
      if (/already exists/i.test(msg)) {
        setForgeMode(false);
        $('#unlock-error').textContent = 'This node already has an account. Log in with the local username.';
        return;
      }
      if (/does not exist|missing/i.test(msg)) {
        setForgeMode(true);
        $('#unlock-error').textContent = 'No account yet. Create the first local account.';
        return;
      }
      $('#unlock-error').textContent = msg;
      return;
    }
    try {
      await loadLibrary();
      await loadCapsules();
      await refreshPlaces();
    } catch (err) {
      toast(err.message);
    }
  }
  e.target.reset();
  openDesk();
  toast(state.forging
    ? (live ? 'Vault forged. No recovery. Cut a kit while you remember.' : 'Vault forged in this preview. No recovery. Cut a kit while you remember.')
    : (live ? 'Vault unlocked. Closing the tab detaches.' : 'Vault unlocked in this preview. Closing the tab would detach.'));
};

$('#upload').onchange = async e => {
  if (!live) return;
  const list = [...e.target.files];
  e.target.value = '';
  const prefix = uploadPrefix;
  uploadPrefix = '';
  beginUpload(list, prefix);
};

$('#upload-folder').onchange = e => { const list = [...e.target.files]; e.target.value = ''; beginUpload(list, uploadPrefix); uploadPrefix = ''; };

document.addEventListener('submit', e => {
  if (e.target.id === 'send-form') { e.preventDefault(); mint(); }
  if (e.target.id === 'places-form') {
    e.preventDefault();
    const data = new FormData(e.target);
    state.grabBase = String(data.get('grab') || '').trim();
    state.driveBase = String(data.get('drive') || '').trim();
    if (live) {
      engine.savePlaces({grab: state.grabBase, drive: state.driveBase})
        .then(() => toast('Places saved. Mint will use the https grab name.'))
        .catch(err => toast(err.message));
    } else {
      toast(state.grabBase.startsWith('https://') ? 'Places saved in this preview. No proxy was contacted.' : 'Grab base must be https:// or mint will refuse.');
    }
    renderMain();
  }
  if (e.target.id === 'destroy-form') {
    e.preventDefault();
    const phrase = String(new FormData(e.target).get('phrase') || '').trim();
    if (phrase !== `DESTROY ${state.destroyId}`) { toast(`Type DESTROY ${state.destroyId} exactly.`); return; }
    revoke(state.destroyId);
    e.target.reset();
  }
});

document.addEventListener('keydown', e => {
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  if ($('#modal').open) return;
  if (!state.unlocked) return;
  if (e.target.matches('input,textarea,select') || e.target.isContentEditable) return;
  if (e.key === 'Escape') hideMenu();
  if (e.key === '?') help();
  if (e.key.toLowerCase() === 'l') lockVault();
  if (e.key.toLowerCase() === 's') {
    if (state.selected) { state.view === 'send' ? mint() : navigate('send'); }
    else navigate('library');
  }
  if (/^[1-5]$/.test(e.key)) navigate(['home', 'library', 'send', 'capsules', 'places'][Number(e.key) - 1]);
});

$('#modal').addEventListener('close', () => { $('#modal-content').replaceChildren(); });
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'hidden') {
    if (frame) cancelAnimationFrame(frame);
    frame = 0;
    return;
  }
  if (state.operation === 'mint' && !frame) frame = requestAnimationFrame(tickMint);
  if (state.operation === 'ingest' && !frame) frame = requestAnimationFrame(tickIngest);
});
renderDeck();
document.addEventListener('contextmenu', e => {
  const file = e.target.closest('[data-ctx-file]');
  const folder = e.target.closest('[data-ctx-folder]');
  const cap = e.target.closest('[data-ctx-capsule]');
  const tree = e.target.closest('[data-ctx-tree]');
  if (file) { e.preventDefault(); showMenu(e.clientX, e.clientY, fileMenu(file.dataset.ctxFile)); return; }
  if (folder) { e.preventDefault(); showMenu(e.clientX, e.clientY, folderMenu(folder.dataset.ctxFolder)); return; }
  if (cap) { e.preventDefault(); showMenu(e.clientX, e.clientY, capsuleMenu(cap.dataset.ctxCapsule)); return; }
  if (tree) { e.preventDefault(); showMenu(e.clientX, e.clientY, rootMenu()); }
});

document.addEventListener('dragstart', e => {
  const file = e.target.closest('[data-drag-file]');
  if (!file || !e.dataTransfer) return;
  e.dataTransfer.effectAllowed = 'move';
  e.dataTransfer.setData('application/x-weazl-path', file.dataset.dragFile);
  file.classList.add('dragging');
});

document.addEventListener('dragend', e => e.target.closest('[data-drag-file]')?.classList.remove('dragging'));

document.addEventListener('dragover', e => {
  const target = e.target.closest('.tree, [data-ctx-folder]');
  if (!target || !e.dataTransfer?.types.includes('application/x-weazl-path')) return;
  e.preventDefault();
  e.dataTransfer.dropEffect = 'move';
  const folder = target.closest('[data-ctx-folder]');
  (folder || target).classList.add('drop-target');
});

document.addEventListener('dragleave', e => e.target.closest('.drop-target')?.classList.remove('drop-target'));

document.addEventListener('drop', e => {
  const target = e.target.closest('.tree, [data-ctx-folder]');
  if (!target || !e.dataTransfer) return;
  const from = e.dataTransfer.getData('application/x-weazl-path');
  if (!from) return;
  e.preventDefault();
  document.querySelectorAll('.drop-target').forEach(el => el.classList.remove('drop-target'));
  const folder = target.closest('[data-ctx-folder]')?.dataset.ctxFolder || state.currentPath;
  const file = files.find(f => filePath(f.id) === from);
  if (file) moveFile(file.id, folder);
});
engine.probe().then(async s => {
  if (!s) {
    seedPreview();
    if (state.unlocked) renderMain();
    renderDeck();
    return;
  }
  live = true;
  state.engine = true;
  state.authenticated = !!s.authenticated;
  const kicker = document.querySelector('#strip-kicker');
  const dim = document.querySelector('#strip-dim') || document.querySelector('.preview-strip .dim');
  if (kicker) kicker.textContent = 'THIS NODE';
  if (dim) dim.textContent = '· engine · this machine';
  await refreshPlaces();
  setForgeMode(!s.setup);
  if (s.authenticated && s.unlocked) {
    await loadLibrary();
    await loadCapsules();
    await refreshPlaces();
    openDesk();
  } else {
    renderDeck();
  }
});
