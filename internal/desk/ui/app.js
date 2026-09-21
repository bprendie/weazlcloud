import {files, filesInFolder, takeouts, state, selectedName, seedPreview, escapeHTML as esc} from './data.js';
import {renderMain, renderSide, renderDeck, renderUploadTray} from './views.js';
import * as engine from './engine.js';

const $ = s => document.querySelector(s);
let noticeTimer, frame, live = false, uploadPrefix = '';
const UPLOAD_RAILS = 3;
const uploadRequests = new Map();
let uploadWorkersRunning = false;
let uploadRefreshTimer;

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

const GRID_PREVIEW_RAILS = 4;
const gridTextQueue = [];
const gridThumbQueue = [];
let gridTextActive = 0;
let gridThumbActive = 0;
const gridPreviewObserver = 'IntersectionObserver' in window
  ? new IntersectionObserver(entries => entries.forEach(entry => {
    if (!entry.isIntersecting) return;
    const el = entry.target;
    gridPreviewObserver.unobserve(el);
    el.dataset.previewQueued = '1';
    if (el.dataset.gridTextPreview !== undefined) gridTextQueue.push(el);
    else gridThumbQueue.push(el);
    pumpGridTextPreviews();
    pumpGridThumbnails();
  }), {rootMargin: '500px 0px'})
  : null;

function pumpGridTextPreviews() {
  while (gridTextActive < 3 && gridTextQueue.length) {
    const el = gridTextQueue.shift();
    if (!el?.isConnected) continue;
    gridTextActive++;
    const path = el.dataset.gridTextPreview || '';
    fetch(`/api/library?path=${encodeURIComponent(path)}&preview=1`)
      .then(response => { if (!response.ok) throw new Error('preview unavailable'); return response.text(); })
      .then(text => { el.textContent = text.slice(0, 1200) || '(empty file)'; })
      .catch(() => { el.textContent = 'Preview unavailable'; el.classList.add('preview-unavailable'); })
      .finally(() => { gridTextActive--; pumpGridTextPreviews(); });
  }
}

function pumpGridThumbnails() {
  while (gridThumbActive < GRID_PREVIEW_RAILS && gridThumbQueue.length) {
    const el = gridThumbQueue.shift();
    if (!el?.isConnected) continue;
    gridThumbActive++;
    const path = el.dataset.gridThumbnail || '';
    el.src = `/api/library/thumbnail?path=${encodeURIComponent(path)}&size=320`;
    const done = () => { gridThumbActive--; pumpGridThumbnails(); };
    el.addEventListener('load', done, {once: true});
    el.addEventListener('error', () => {
      el.replaceWith(Object.assign(document.createElement('div'), {className: 'grid-kind', textContent: 'Preview unavailable'}));
      done();
    }, {once: true});
  }
}

function hydrateGridTextPreviews() {
  const observe = (el, queue) => {
    if (el.dataset.previewQueued !== undefined || el.dataset.previewObserved !== undefined) return;
    if (gridPreviewObserver) {
      el.dataset.previewObserved = '1';
      gridPreviewObserver.observe(el);
      return;
    }
    el.dataset.previewQueued = '1';
    queue.push(el);
  };
  document.querySelectorAll('[data-grid-text-preview]').forEach(el => observe(el, gridTextQueue));
  document.querySelectorAll('[data-grid-thumbnail]').forEach(el => observe(el, gridThumbQueue));
  pumpGridTextPreviews();
  pumpGridThumbnails();
}

const contentObserver = new MutationObserver(() => { hydrateGridTextPreviews(); });
contentObserver.observe($('#content'), {childList: true});

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
  const previewable = new Set(['IMG', 'JPG', 'JPEG', 'PNG', 'GIF', 'WEB', 'WEBP', 'SVG', 'PDF', 'TXT', 'MD', 'CSV', 'JSON', 'DOC', 'DOCX', 'XLS', 'XLSX', 'PPT', 'PPTX', 'ODT', 'ODS', 'ODP', 'STL', '3MF', 'MP3', 'WAV', 'FLAC', 'M4A', 'AAC', 'OGG', 'OGA', 'FLA', 'MP4', 'MOV', 'WEBM', 'MKV', 'AVI', 'M4V']);
  if (!previewable.has(String(f.kind).toUpperCase())) { toast('This file opens as a download.'); return; }
  const mediaKind = new Set(['MP3', 'WAV', 'FLAC', 'M4A', 'AAC', 'OGG', 'OGA', 'MP4', 'MOV', 'WEBM', 'MKV', 'AVI', 'M4V']);
  if (mediaKind.has(String(f.kind).toUpperCase())) {
    const mediaURL = `/api/library?path=${encodeURIComponent(path)}&inline=1`;
    const isVideo = ['MP4', 'MOV', 'WEBM', 'MKV', 'AVI', 'M4V'].includes(String(f.kind).toUpperCase());
    const tag = isVideo ? 'video' : 'audio';
    modal(`<span class="eyebrow purple">PREVIEW / ${isVideo ? 'VIDEO' : 'AUDIO'}</span><h2>${esc(f.title)}</h2><${tag} class="file-preview-media" src="${mediaURL}" controls preload="metadata"></${tag}><div class="preview-actions"><a class="secondary button-link" href="${mediaURL}" download="${esc(f.title)}">Download</a></div><p class="eyebrow">${esc(path)}</p>`, true);
    return;
  }
  try {
    const result = await engine.previewLibrary(path);
    const url = URL.createObjectURL(result.blob);
    const type = result.type.split(';')[0];
    let body;
    if (type.startsWith('image/')) body = `<img class="file-preview-image" src="${url}" alt="${esc(f.title)}">`;
    else if (type.startsWith('text/') || ['application/json', 'application/xml', 'application/javascript', 'application/x-yaml'].includes(type)) body = `<pre class="file-preview-text">${esc(await result.blob.text())}</pre>`;
    else if (type === 'application/pdf') body = `<iframe class="file-preview-frame" src="${url}" title="${esc(f.title)}"></iframe>`;
    else if (type.startsWith('audio/')) body = `<audio class="file-preview-media" src="${url}" controls preload="metadata"></audio>`;
    else if (type.startsWith('video/')) body = `<video class="file-preview-media" src="${url}" controls preload="metadata"></video>`;
    else { URL.revokeObjectURL(url); toast('This file opens as a download.'); return; }
    modal(`<span class="eyebrow purple">PREVIEW / ${esc(type)}</span><h2>${esc(f.title)}</h2>${body}<div class="preview-actions"><a class="secondary button-link" href="${url}" download="${esc(f.title)}">Download</a></div><p class="eyebrow">${esc(path)}</p>`, true);
    const image = $('#modal .file-preview-image');
    image?.addEventListener('error', () => {
      image.replaceWith(Object.assign(document.createElement('p'), {className: 'preview-error', textContent: 'The stored bytes could not be decoded as an image.'}));
    }, {once: true});
    $('#modal').addEventListener('close', () => URL.revokeObjectURL(url), {once: true});
  } catch (err) { toast(err.message); }
}

function newUploadID() {
  return globalThis.crypto?.randomUUID?.() || `upload-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function uploadState() {
  if (!state.upload.items) state.upload.items = [];
  if (!state.upload.rails?.length) state.upload.rails = Array.from({length: UPLOAD_RAILS}, () => ({name: 'Waiting…', pct: 0, status: 'waiting'}));
  return state.upload;
}

function refreshUploadSummary() {
  const upload = uploadState();
  upload.total = upload.items.length;
  upload.totalBytes = upload.items.reduce((n, item) => n + item.size, 0);
  upload.done = upload.items.filter(item => item.status === 'done').length;
  upload.failed = upload.items.filter(item => item.status === 'failed');
  upload.loaded = upload.items.reduce((n, item) => n + Math.min(item.loaded || 0, item.size), 0);
  upload.percent = upload.totalBytes ? (upload.loaded / upload.totalBytes) * 100 : (upload.total ? 100 : 0);
  upload.active = upload.items.some(item => item.status === 'queued' || item.status === 'uploading' || item.status === 'saving');
  upload.current = upload.items.find(item => item.status === 'uploading')?.target || '';
  renderUploadTray();
}

function scheduleUploadRefresh() {
  clearTimeout(uploadRefreshTimer);
  uploadRefreshTimer = setTimeout(async () => {
    if (!live) return;
    try { await loadLibrary(); renderMain(); renderDeck(); } catch (err) { toast(err.message); }
  }, 180);
}

function uploadItemForSlot(slot) {
  const upload = uploadState();
  const item = upload.items.find(candidate => candidate.status === 'queued');
  if (!item) return null;
  item.status = 'uploading';
  item.slot = slot;
  upload.rails[slot] = {name: item.target, pct: item.size ? (item.loaded / item.size) * 100 : 0, status: '0%'};
  refreshUploadSummary();
  return item;
}

async function uploadWorker(slot) {
  while (true) {
    const item = uploadItemForSlot(slot);
    if (!item) return;
    try {
      const request = engine.putLibraryProgress(item.target, item.file, (sent, _total, phase) => {
        item.loaded = sent;
        const pct = item.size ? (sent / item.size) * 100 : 100;
        item.status = phase === 'saving' ? 'saving' : 'uploading';
        state.upload.rails[slot] = {name: item.target, pct, status: phase || `${Math.round(pct)}%`};
        refreshUploadSummary();
      });
      uploadRequests.set(item.id, request);
      await request;
      item.loaded = item.size;
      item.status = 'done';
      item.error = '';
      state.upload.rails[slot] = {name: `✓ ${item.target}`, pct: 100, status: 'done'};
      scheduleUploadRefresh();
    } catch (err) {
      if (item.status === 'cancelled') {
        state.upload.rails[slot] = {name: `× ${item.target}`, pct: item.size ? (item.loaded / item.size) * 100 : 0, status: 'cancelled'};
      } else {
        item.status = 'failed';
        item.error = err.message;
        state.upload.rails[slot] = {name: `× ${item.target}`, pct: item.size ? (item.loaded / item.size) * 100 : 0, status: 'failed'};
      }
    } finally {
      uploadRequests.delete(item.id);
      item.slot = null;
      refreshUploadSummary();
    }
  }
}

async function runUploadQueue() {
  if (uploadWorkersRunning) return;
  uploadWorkersRunning = true;
  try {
    await Promise.all(Array.from({length: UPLOAD_RAILS}, (_, slot) => uploadWorker(slot)));
  } finally {
    uploadWorkersRunning = false;
    refreshUploadSummary();
    if (!state.upload.active && state.upload.total) {
      scheduleUploadRefresh();
      const failures = state.upload.failed;
      toast(failures.length ? `${failures.length} upload${failures.length === 1 ? '' : 's'} failed.` : 'Upload complete.');
    }
  }
}

function beginUpload(list, prefix = '') {
  const filesToUpload = [...list].map(item => item.file
    ? item
    : {file: item, relative: item.webkitRelativePath || item.name});
  if (!filesToUpload.length) return;
  const upload = uploadState();
  upload.dismissed = false;
  upload.collapsed = false;
  upload.items.push(...filesToUpload.map(item => {
    const file = item.file;
    const relative = item.relative || file.name;
    return {id: newUploadID(), file, relative, target: [prefix, relative].filter(Boolean).join('/'), size: file.size, loaded: 0, status: 'queued', attempts: 0, error: ''};
  }));
  refreshUploadSummary();
  runUploadQueue();
}

function retryFailedUploads() {
  const upload = uploadState();
  upload.items.filter(item => item.status === 'failed').forEach(item => { item.status = 'queued'; item.loaded = 0; item.error = ''; item.attempts = (item.attempts || 0) + 1; });
  upload.dismissed = false;
  refreshUploadSummary();
  runUploadQueue();
}

function cancelUploads() {
  const upload = uploadState();
  upload.items.filter(item => item.status === 'queued').forEach(item => { item.status = 'cancelled'; });
  uploadRequests.forEach((request, id) => {
    const item = upload.items.find(candidate => candidate.id === id);
    if (item) { item.status = 'cancelled'; request.abort?.(); }
  });
  refreshUploadSummary();
}

function readDirectoryEntries(entry) {
  return new Promise((resolve, reject) => {
    const reader = entry.createReader();
    const entries = [];
    const read = () => reader.readEntries(batch => {
      if (!batch.length) return resolve(entries);
      entries.push(...batch);
      read();
    }, reject);
    read();
  });
}

function readDroppedEntry(entry, prefix = '') {
  if (entry.isFile) {
    return new Promise((resolve, reject) => entry.file(file => resolve([{file, relative: [prefix, file.name].filter(Boolean).join('/')}]), reject));
  }
  if (!entry.isDirectory) return Promise.resolve([]);
  return readDirectoryEntries(entry).then(entries => Promise.all(entries.map(child => readDroppedEntry(child, [prefix, entry.name].filter(Boolean).join('/')))).then(groups => groups.flat()));
}

async function droppedUploadItems(dataTransfer) {
  const entries = [...dataTransfer.items]
    .filter(item => item.kind === 'file' && item.webkitGetAsEntry)
    .map(item => item.webkitGetAsEntry())
    .filter(Boolean);
  if (!entries.length) return [...dataTransfer.files].map(file => ({file, relative: file.name}));
  return (await Promise.all(entries.map(entry => readDroppedEntry(entry)))).flat();
}

function capsuleMenu(id) {
  const cap = state.capsules.find(c => c.id === id);
  if (!cap || cap.status !== 'live') return [{act: `open-grab:${id}`, label: 'This grab is gone'}];
  return [
    {act: `copy-capsule:${id}`, label: 'Copy grab URL'},
    {act: `open-grab:${id}`, label: 'Open as recipient'},
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

function modal(html, wide = false) {
  $('#modal').classList.toggle('wide', wide);
  $('#modal-content').innerHTML = html;
  $('#modal').showModal();
}

function help() {
  modal('<span class="eyebrow purple">KEEP YOUR HANDS ON THE KEYS</span><h2>The short route.</h2>' +
    [['Home / Library / Send / Capsules / Places', '1–5'], ['Mint grab link', 'S'], ['Lock vault', 'L'], ['File / folder actions', 'Right-click or ⋯'], ['This cheat sheet', '?']].map(([a, b]) => `<div class="shortcut"><span>${a}</span><kbd>${b}</kbd></div>`).join(''));
}

function vaultCard() {
  modal(`<img class="auth-brand" src="weazlcloud.png" alt="WeazlCloud"><span class="eyebrow purple">ACCOUNT SETTINGS</span><h2>${esc(state.fullName || state.username || 'Your account')}</h2><p>Local identity and vault controls. The administrator cannot open this user's vault.</p><form id="settings-form" class="auth-form"><label>Full name<input name="full_name" value="${esc(state.fullName)}" maxlength="120" placeholder="Your full name"></label><label>Current account password<input name="current_password" type="password" autocomplete="current-password"></label><label>New account password<input name="new_password" type="password" autocomplete="new-password"></label><button class="primary">Save settings</button></form><hr><h3>Rekey vault</h3><p class="eyebrow">Changes the vault passphrase while preserving the library.</p><form id="rekey-form" class="auth-form"><label>Current vault passphrase<input name="current" type="password" required></label><label>New vault passphrase<input name="next" type="password" required></label><label>Confirm new vault passphrase<input name="confirm" type="password" required></label><button class="secondary">Rekey vault</button></form><div class="dialog-actions"><button class="secondary" id="do-lock">${state.unlocked ? 'Lock vault' : 'Unlock…'}</button></div>` , true);
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
    label: state.label || 'recipient',
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
  if (state.gate === 'passphrase' && !state.passphrase) { toast('Give recipient a passphrase, or switch to Open.'); return; }
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
      toast('Grab link minted. Sealed copy. recipient cannot write back.');
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
  await loadQuota();
}

async function loadQuota() {
  if (!live) return;
  try { state.quota = await engine.quota(); } catch (err) { toast(err.message); }
}

function openDesk() {
  state.unlocked = true;
  $('#unlock-screen').hidden = true;
  $('.app').hidden = false;
  $('#admin-nav').hidden = !state.admin;
  if (state.username) $('#username').innerHTML = `${esc(state.fullName || state.username)}<small>Open · this session owns the key</small>`;
  renderMain();
  renderDeck();
}

async function loadAccessRequests() {
  if (!live || !state.admin) return;
  try { state.requests = await engine.listAccessRequests(); } catch (err) { toast(err.message); }
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

async function refreshNodeSettings() {
  if (!state.admin || !live) return;
  const node = await engine.loadNodeSettings().catch(() => null);
  if (node?.hostname) { state.nodeHostname = node.hostname; state.grabBase = `https://${node.hostname}`; }
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
  if (e.target.closest('.grid-media-player')) return;
  const b = e.target.closest('button, a.button-link, [role="button"]');
  if (!b) return;
  if (b.classList.contains('dialog-close') || b.dataset.close !== undefined) { $('#modal').close(); return; }
  if (b.dataset.dismissUpload !== undefined) { state.upload.dismissed = true; renderUploadTray(); return; }
  if (b.dataset.uploadCollapse !== undefined) { state.upload.collapsed = !state.upload.collapsed; renderUploadTray(); return; }
  if (b.dataset.uploadRetry !== undefined) { retryFailedUploads(); return; }
  if (b.dataset.uploadCancel !== undefined) { cancelUploads(); return; }
  if (b.closest('form') && !b.dataset.action) return;
  if (b.dataset.view) navigate(b.dataset.view);
  if (b.dataset.openFolder) { state.currentPath = b.dataset.openFolder; state.selected = null; renderMain(); renderDeck(); }
  if (b.dataset.libraryPath !== undefined) { state.currentPath = b.dataset.libraryPath; state.selected = null; renderMain(); renderDeck(); }
  if (b.dataset.librarySortDir !== undefined) { state.librarySortDir = state.librarySortDir === 'asc' ? 'desc' : 'asc'; renderMain(); }
  if (b.dataset.libraryView !== undefined) { state.libraryView = b.dataset.libraryView; renderMain(); }
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
  if (b.dataset.adminApprove) {
    engine.approveAccess(b.dataset.adminApprove).then(result => {
      modal(`<span class="eyebrow purple">ADMIN / APPROVAL COMPLETE</span><h2>Give this setup token to ${esc(result.request.username)}</h2><p>This token can be used once to create the approved local account and vault.</p><pre class="file-preview-text">${esc(result.setup_token)}</pre><button class="primary" data-copy-token="${esc(result.setup_token)}">Copy token</button><button class="secondary" data-close>Done</button>`, true);
      loadAccessRequests().then(renderMain);
    }).catch(err => toast(err.message));
  }
  if (b.dataset.adminReject) engine.rejectAccess(b.dataset.adminReject).then(() => loadAccessRequests().then(renderMain)).catch(err => toast(err.message));
  if (b.dataset.copyToken) { navigator.clipboard?.writeText(b.dataset.copyToken).catch(() => {}); toast('Setup token copied.'); }
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
  if (e.target.dataset.librarySort !== undefined) {
    state.librarySort = e.target.value;
    renderMain();
  }
});

document.addEventListener('input', e => {
  if (e.target.name === 'phrase' && e.target.closest('#send-form')) state.passphrase = e.target.value;
  if (e.target.name === 'label') state.label = e.target.value;
  if (e.target.name === 'expiry') state.expiry = e.target.value;
  if (e.target.name === 'grabs') state.grabs = e.target.value;
  if (e.target.dataset.librarySearch !== undefined) {
    const caret = e.target.selectionStart;
    state.librarySearch = e.target.value;
    renderMain();
    const search = document.querySelector('[data-library-search]');
    search?.focus();
    search?.setSelectionRange(caret, caret);
  }
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
  setVaultOnly(false);
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

function setVaultOnly(on) {
  const row = $('#username-row');
  const username = $('#username-row input');
  const forge = $('#forge-mode');
  if (row) row.hidden = on;
  if (username) username.disabled = on;
  if (forge) forge.hidden = on;
  $('#unlock-description').textContent = on
    ? 'Account signed in. Unlock this user vault. The vault passphrase never leaves this machine.'
    : 'Unlock the vault. The passphrase never leaves this machine.';
  $('#unlock-form .primary').textContent = on ? 'Unlock vault →' : (state.forging ? 'Create account →' : 'Log in →');
}

$('#forge-mode').onclick = () => setForgeMode(!state.forging);
$('#request-mode').onclick = async () => {
  const username = String($('#unlock-form [name=username]').value || '').trim();
  if (!username) { $('#unlock-error').textContent = 'Enter the username you want to request.'; return; }
  const note = prompt('Optional note for the node administrator:', '') || '';
  if (!live) { $('#unlock-error').textContent = 'Access requests are available on the connected node.'; return; }
  try { const q = await engine.requestAccess(username, note); $('#unlock-error').textContent = `Request submitted for ${q.username}. An administrator must approve it before an account or vault is created.`; }
  catch (err) { $('#unlock-error').textContent = err.message; }
};
$('#setup-mode').onclick = async () => {
  if (!live) { $('#unlock-error').textContent = 'Account setup is available on the connected node.'; return; }
  const username = String($('#unlock-form [name=username]').value || '').trim();
  const id = prompt('Approval request ID:') || '';
  const token = prompt('One-time setup token:') || '';
  const password = prompt('Choose an account password (8+ characters):') || '';
  const vault = prompt('Vault passphrase (leave blank to use the account password):', '') || '';
  if (!username || !id || !token || !password) { $('#unlock-error').textContent = 'Setup cancelled: all account fields are required.'; return; }
  try { await engine.completeAccess({id, token, username, password, vault_passphrase: vault, confirm: vault || password}); $('#unlock-error').textContent = 'Account created. Log in with your local username and password.'; }
  catch (err) { $('#unlock-error').textContent = err.message; }
};

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
        state.admin = true;
        state.username = username;
        await engine.unlock(pass);
      } else if (!state.authenticated) {
        const account = await engine.login(username, pass);
        state.admin = !!account.admin;
        state.username = account.username || username;
        state.authenticated = true;
        try {
          await engine.unlock(pass);
        } catch {
          setVaultOnly(true);
          e.target.querySelector('[name=passphrase]').value = '';
          return;
        }
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
      await loadAccessRequests();
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
    state.driveBase = String(data.get('drive') || '').trim();
    if (live) {
      engine.savePlaces({drive: state.driveBase})
        .then(() => toast('Files mount address saved. The administrator owns the grab hostname.'))
        .catch(err => toast(err.message));
    } else {
      toast('Files mount address saved in this preview.');
    }
    renderMain();
  }
  if (e.target.id === 'node-settings-form') {
    e.preventDefault();
    const hostname = String(new FormData(e.target).get('hostname') || '').trim();
    if (!live) { state.nodeHostname = hostname; state.grabBase = hostname ? `https://${hostname}` : ''; renderMain(); toast('Node hostname saved in this preview.'); return; }
    engine.saveNodeSettings(hostname).then(result => { state.nodeHostname = result.hostname; state.grabBase = `https://${result.hostname}`; renderMain(); toast('Node hostname saved.'); }).catch(err => toast(err.message));
  }
  if (e.target.id === 'destroy-form') {
    e.preventDefault();
    const phrase = String(new FormData(e.target).get('phrase') || '').trim();
    if (phrase !== `DESTROY ${state.destroyId}`) { toast(`Type DESTROY ${state.destroyId} exactly.`); return; }
    revoke(state.destroyId);
    e.target.reset();
  }
  if (e.target.id === 'settings-form') {
    e.preventDefault(); const data = new FormData(e.target); const body = Object.fromEntries(data.entries());
    if (!live) { toast('Settings are available on the connected node.'); return; }
    engine.saveSettings(body).then(result => { state.fullName = result.full_name || ''; $('#username').innerHTML = `${esc(state.fullName || result.username)}<small>Open · this session owns the key</small>`; toast('Account settings saved.'); }).catch(err => toast(err.message));
  }
  if (e.target.id === 'rekey-form') {
    e.preventDefault(); const data = new FormData(e.target); const current = String(data.get('current') || ''), next = String(data.get('next') || ''), confirm = String(data.get('confirm') || '');
    if (next !== confirm) { toast('New vault passphrases do not match.'); return; }
    if (!live) { toast('Vault rekeying is available on the connected node.'); return; }
    engine.rekeyVault(current, next, confirm).then(() => { e.target.reset(); toast('Vault rekeyed.'); }).catch(err => toast(err.message));
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

$('#modal').addEventListener('close', () => { $('#modal-content').replaceChildren(); $('#modal').classList.remove('wide'); });
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
  const libraryContent = state.view === 'library' && e.target.closest('#content');
  if (file) { e.preventDefault(); showMenu(e.clientX, e.clientY, fileMenu(file.dataset.ctxFile)); return; }
  if (folder) { e.preventDefault(); showMenu(e.clientX, e.clientY, folderMenu(folder.dataset.ctxFolder)); return; }
  if (cap) { e.preventDefault(); showMenu(e.clientX, e.clientY, capsuleMenu(cap.dataset.ctxCapsule)); return; }
  if (tree || libraryContent) { e.preventDefault(); showMenu(e.clientX, e.clientY, rootMenu()); }
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
  const folder = e.target.closest('[data-ctx-folder]');
  const target = folder || (state.view === 'library'
    ? e.target.closest('#content')
    : e.target.closest('.library-workspace, .tree'));
  if (!target || !e.dataTransfer) return;
  const internal = e.dataTransfer.types.includes('application/x-weazl-path');
  const external = e.dataTransfer.types.includes('Files') || [...(e.dataTransfer.items || [])].some(item => item.kind === 'file');
  if (!internal && !external) return;
  e.preventDefault();
  e.dataTransfer.dropEffect = internal ? 'move' : 'copy';
  (folder || target).classList.add('drop-target');
});

document.addEventListener('dragleave', e => e.target.closest('.drop-target')?.classList.remove('drop-target'));

document.addEventListener('drop', e => {
  const folderTarget = e.target.closest('[data-ctx-folder]');
  const target = folderTarget || (state.view === 'library'
    ? e.target.closest('#content')
    : e.target.closest('.library-workspace, .tree'));
  if (!target || !e.dataTransfer) return;
  const from = e.dataTransfer.getData('application/x-weazl-path');
  const hasExternalFiles = e.dataTransfer.files.length || [...(e.dataTransfer.items || [])].some(item => item.kind === 'file');
  if (!from && hasExternalFiles) {
    e.preventDefault();
    document.querySelectorAll('.drop-target').forEach(el => el.classList.remove('drop-target'));
    const folder = folderTarget?.dataset.ctxFolder || state.currentPath;
    droppedUploadItems(e.dataTransfer).then(items => beginUpload(items, folder)).catch(err => toast(`Drop failed: ${err.message}`));
    return;
  }
  if (!from) return;
  e.preventDefault();
  document.querySelectorAll('.drop-target').forEach(el => el.classList.remove('drop-target'));
  const folder = folderTarget?.dataset.ctxFolder || state.currentPath;
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
  state.admin = !!s.admin;
  $('#admin-nav').hidden = !state.admin;
  setForgeMode(!s.setup);
  await refreshNodeSettings();
  if (s.authenticated && s.unlocked) {
    const account = await engine.me().catch(() => null); if (account) { state.username = account.username || state.username; state.fullName = account.full_name || ''; state.admin = !!account.admin; }
    await loadLibrary();
    await loadCapsules();
    await refreshPlaces();
    await loadAccessRequests();
    openDesk();
  } else {
    renderDeck();
  }
});
