import {layoutPhotos} from './photo-layout.js';
import {installPhotoTimeline} from './photo-timeline.js';
import {files, filesInFolder, takeouts, state, selectedName, seedPreview, escapeHTML as esc} from './data.js';
import {renderMain, renderDeck, renderUploadTray, refreshPhotoGrid, modifiedLabel} from './views.js';
import * as engine from './engine.js';
import {ModeMemory} from './mode-memory.js';
const modeMemory = new ModeMemory();

const $ = s => document.querySelector(s);
let noticeTimer, frame, live = false, uploadPrefix = '';
let libraryEventSource;
let librarySyncRunning = false;
let librarySyncAgain = false;
const UPLOAD_RAILS = 3;
const UPLOAD_CHUNK_SIZE = 8 * 1024 * 1024;
const uploadRequests = new Map();
let uploadWorkersRunning = false;
let uploadRefreshTimer;
let uploadPersistTimer;
let uploadStorageKey = '';
let lastTakeoutComplete = '';

function libraryPath() {
  if (!state.selected) return '';
  if (state.selected.type === 'folder') return state.selected.path;
  const f = files.find(x => x.id === state.selected.id);
  if (!f) return '';
  return f.folders.concat(f.title).join('/');
}

function filePath(id) {
  const f = files.find(x => x.id === id) || state.photoItems?.find(x => x.id === id) || state.photoSelectedItems?.get(id);
  return f ? f.folders.concat(f.title).filter(Boolean).join('/') : id;
}

function fileByID(id) {
  return files.find(file => file.id === id) || state.photoItems?.find(file => file.id === id) || state.photoSelectedItems?.get(id);
}

function selectedFileRows() {
  const ids = state.selectedFiles?.length ? state.selectedFiles : state.selected?.type === 'file' ? [state.selected.id] : [];
  return ids.map(fileByID).filter(Boolean);
}

function clearFileSelection() {
  state.photoSelection = null;
  state.photoSelectedItems = new Map();
  state.selectedFiles = [];
  state.selectionAnchor = '';
  state.selected = null;
}

function hideMenu() {
  const el = $('#ctx');
  if (el) el.hidden = true;
}

const GRID_PREVIEW_RAILS = 4;
const gridTextQueue = [];
const gridThumbQueue = [];
const gridCapabilityQueue = [];
const gridMusicQueue = [];
let gridMusicActive = 0;
const gridPreviewRequests = new Map();
let photoWindowFrame = 0;
let gridTextActive = 0;
let gridThumbActive = 0;
let gridCapabilityActive = 0;
const previewWarmQueue = [];
let previewWarmActive = 0;
let previewWarmTimer = 0;
let previewWarmController = null;
const gridPreviewObserver = 'IntersectionObserver' in window
  ? new IntersectionObserver(entries => entries.forEach(entry => {
    if (!entry.isIntersecting) return;
    const el = entry.target;
    gridPreviewObserver.unobserve(el);
    el.dataset.previewQueued = '1';
    if (el.dataset.gridMusic !== undefined) gridMusicQueue.push(el);
    else if (el.dataset.gridCapability !== undefined) gridCapabilityQueue.push(el);
    else if (el.dataset.gridTextPreview !== undefined) gridTextQueue.push(el);
    else gridThumbQueue.push(el);
    pumpGridCapabilities();
    pumpGridTextPreviews();
    pumpGridThumbnails();
    pumpGridMusic();
  }), {rootMargin: '500px 0px'})
  : null;
const photoMoreObserver = 'IntersectionObserver' in window
  ? new IntersectionObserver(entries => {
    if (entries.some(entry => entry.isIntersecting) && state.photoHasMore && !state.photoLoading) loadPhotoPage(false);
  }, {rootMargin: '900px 0px'})
  : null;

function pumpGridTextPreviews() {
  while (gridTextActive < 3 && gridTextQueue.length) {
    const el = gridTextQueue.shift();
    if (!el?.isConnected) continue;
    gridTextActive++;
    const path = el.dataset.gridTextPreview || '';
    const controller = new AbortController();
    gridPreviewRequests.set(el, {controller});
    fetch(`/api/library?path=${encodeURIComponent(path)}&preview=1`)
      .then(response => { if (!response.ok) throw new Error('preview unavailable'); return response.text(); })
      .then(text => { el.textContent = text.slice(0, 1200) || '(empty file)'; })
      .catch(err => { if (err.name !== 'AbortError' && el.isConnected) { el.textContent = 'Preview unavailable'; el.classList.add('preview-unavailable'); } })
      .finally(() => { gridPreviewRequests.delete(el); gridTextActive--; pumpGridTextPreviews(); });
  }
}

function pumpGridThumbnails() {
  while (gridThumbActive < GRID_PREVIEW_RAILS && gridThumbQueue.length) {
    const el = gridThumbQueue.shift();
    if (!el?.isConnected) continue;
    gridThumbActive++;
    const path = el.dataset.gridThumbnail || '';
    const photoID = el.dataset.photoThumbnail || '';
    const controller = new AbortController();
    let settled = false;
    const done = () => {
      if (settled) return;
      settled = true;
      gridPreviewRequests.delete(el);
      gridThumbActive--;
      pumpGridThumbnails();
    };
    gridPreviewRequests.set(el, {controller, cancel: () => { el.src = ''; done(); }});
    el.src = photoID ? `/api/v1/photos/assets/${encodeURIComponent(photoID)}/thumbnail?size=320${state.photosMode === 'hidden' ? '&hidden=1' : ''}` : `/api/library/thumbnail?path=${encodeURIComponent(path)}&size=320`;
    el.addEventListener('load', done, {once: true});
    el.addEventListener('error', () => {
      if (el.isConnected) el.replaceWith(Object.assign(document.createElement('div'), {className: 'grid-kind', textContent: 'Preview unavailable'}));
      done();
    }, {once: true});
  }
}

function pumpGridMusic() {
  while (gridMusicActive < 2 && gridMusicQueue.length) {
    const el = gridMusicQueue.shift();
    if (!el?.isConnected) continue;
    gridMusicActive++;
    const controller = new AbortController();
    gridPreviewRequests.set(el, {controller});
    fetch(`/api/library/music?path=${encodeURIComponent(el.dataset.gridMusic)}`, {signal: controller.signal})
      .then(response => { if (!response.ok) throw new Error('music details unavailable'); return response.json(); })
      .then(tags => {
        if (!el.isConnected) return;
        const cover = el.querySelector('[data-music-cover]');
        if (cover && String(tags.artwork || '').startsWith('data:image/png;base64,')) {
          const bytes = Uint8Array.from(atob(tags.artwork.split(',')[1]), ch => ch.charCodeAt(0));
          const url = URL.createObjectURL(new Blob([bytes], {type: 'image/png'}));
          cover.addEventListener('load', () => { URL.revokeObjectURL(url); cover.hidden = false; cover.previousElementSibling.hidden = true; }, {once: true});
          cover.addEventListener('error', () => URL.revokeObjectURL(url), {once: true});
          cover.src = url;
        }
        const details = el.closest('.library-card')?.querySelector('[data-music-details]');
        if (!details) return;
        const lines = [tags.title, tags.artist || tags.album_artist, tags.album].filter(Boolean);
        for (const [i, line] of lines.entries()) {
          const text = document.createElement(i === 0 && tags.title ? 'strong' : 'span');
          text.textContent = line;
          text.title = line;
          details.append(text);
        }
        const extra = [tags.genre, tags.year, tags.track ? `Track ${tags.track}` : ''].filter(Boolean).join(' · ');
        if (extra) { const text = document.createElement('span'); text.textContent = extra; text.title = extra; details.append(text); }
        details.hidden = !details.childElementCount;
      })
      .catch(() => {}) // Missing, invalid or unsupported tags keep the music icon.
      .finally(() => { gridPreviewRequests.delete(el); gridMusicActive--; pumpGridMusic(); });
  }
}

function capabilityFallback(el) {
  el.removeAttribute('data-grid-capability');
  el.classList.add('preview-unavailable');
}

function applyGridCapability(el, capability) {
  const path = capability.path || el.dataset.gridCapability || '';
  const encoded = encodeURIComponent(path);
  let replacement;
  if (capability.kind === 'thumbnail') {
    replacement = document.createElement('img');
    replacement.className = 'grid-preview';
    replacement.dataset.gridThumbnail = path;
    replacement.alt = '';
    replacement.loading = 'lazy';
  } else if (capability.kind === 'text') {
    replacement = document.createElement('div');
    replacement.className = 'grid-text-preview';
    replacement.dataset.gridTextPreview = path;
    replacement.textContent = 'Loading preview…';
  } else if (capability.kind === 'model') {
    replacement = document.createElement('img');
    replacement.className = 'grid-preview';
    replacement.src = `/api/library?path=${encoded}&preview=1`;
    replacement.alt = '';
    replacement.loading = 'lazy';
  } else if (capability.kind === 'pdf') {
    replacement = document.createElement('div');
    replacement.className = 'grid-kind';
    const label = document.createElement('span');
    label.className = 'kind';
    label.textContent = capability.label || 'PDF';
    replacement.append(label);
  } else if (capability.kind === 'media') {
    const isVideo = String(capability.content_type || '').startsWith('video/');
    replacement = document.createElement(isVideo ? 'video' : 'audio');
    replacement.className = 'grid-media-player';
    replacement.dataset.mediaPath = path;
    replacement.src = `/api/library?path=${encoded}&inline=1`;
    replacement.controls = true;
    replacement.preload = 'metadata';
  } else {
    replacement = document.createElement('div');
    replacement.className = 'grid-kind';
    const label = document.createElement('span');
    label.className = 'kind';
    label.textContent = capability.label || 'FILE';
    replacement.append(label);
  }
  el.replaceWith(replacement);
}

function pumpGridCapabilities() {
  while (gridCapabilityActive < 2 && gridCapabilityQueue.length) {
    const el = gridCapabilityQueue.shift();
    if (!el?.isConnected) continue;
    gridCapabilityActive++;
    const path = el.dataset.gridCapability || '';
    const controller = new AbortController();
    gridPreviewRequests.set(el, {controller});
    fetch(`/api/library/capability?path=${encodeURIComponent(path)}`, {signal: controller.signal})
      .then(response => { if (!response.ok) throw new Error('capability unavailable'); return response.json(); })
      .then(capability => applyGridCapability(el, capability))
      .catch(err => { if (err.name !== 'AbortError' && el.isConnected) capabilityFallback(el); })
      .finally(() => { gridPreviewRequests.delete(el); gridCapabilityActive--; hydrateGridTextPreviews(); pumpGridCapabilities(); });
  }
}

function cancelDetachedGridRequests() {
  for (const [el, request] of gridPreviewRequests) {
    if (!el.isConnected) {
      request.controller?.abort();
      request.cancel?.();
    }
  }
}

function hydratePhotoPager() {
  const button = document.querySelector('[data-photo-load-more]');
  if (button && photoMoreObserver && button.dataset.photoObserved !== '1') {
    button.dataset.photoObserved = '1';
    photoMoreObserver.observe(button);
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
  document.querySelectorAll('[data-grid-capability]').forEach(el => observe(el, gridCapabilityQueue));
  document.querySelectorAll('[data-grid-text-preview]').forEach(el => observe(el, gridTextQueue));
  document.querySelectorAll('[data-grid-thumbnail]').forEach(el => observe(el, gridThumbQueue));
  document.querySelectorAll('[data-photo-thumbnail]').forEach(el => observe(el, gridThumbQueue));
  document.querySelectorAll('[data-grid-music]').forEach(el => observe(el, gridMusicQueue));
  pumpGridMusic();
  pumpGridCapabilities();
  pumpGridTextPreviews();
  pumpGridThumbnails();
}

function updatePhotoGridWindow() {
  photoWindowFrame = 0;
  refreshPhotoGrid();
  photoTimeline.sync();
}

function schedulePhotoGridWindow() {
  if (photoWindowFrame || state.view !== 'photos') return;
  photoWindowFrame = requestAnimationFrame(updatePhotoGridWindow);
}
window.addEventListener('scroll', schedulePhotoGridWindow, {passive: true});
window.addEventListener('resize', schedulePhotoGridWindow, {passive: true});

const contentObserver = new MutationObserver(() => { cancelDetachedGridRequests(); hydrateGridTextPreviews(); hydratePhotoPager(); schedulePhotoGridWindow(); });
contentObserver.observe($('#content'), {childList: true, subtree: true});

function isWarmableRaster(path) {
  return /\.(?:jpe?g|png|gif)$/i.test(path);
}

function stopPreviewWarming() {
  previewWarmQueue.length = 0;
  clearTimeout(previewWarmTimer);
  previewWarmTimer = 0;
  previewWarmController?.abort();
  previewWarmController = null;
}

function pumpPreviewWarm() {
  previewWarmTimer = 0;
  if (!live || !state.unlocked || state.upload.active) {
    if (previewWarmQueue.length) previewWarmTimer = setTimeout(pumpPreviewWarm, 1500);
    return;
  }
  while (previewWarmActive < 1 && previewWarmQueue.length) {
    const path = previewWarmQueue.shift();
    previewWarmActive++;
    previewWarmController = new AbortController();
    fetch(`/api/library/thumbnail?path=${encodeURIComponent(path)}&size=320`, {signal: previewWarmController.signal})
      .catch(() => {})
      .finally(() => {
        previewWarmActive--;
        previewWarmController = null;
        if (previewWarmQueue.length) previewWarmTimer = setTimeout(pumpPreviewWarm, 150);
      });
  }
}

function schedulePreviewWarm(paths) {
  if (!live || !state.unlocked) return;
  for (const path of paths) {
    if (isWarmableRaster(path) && !previewWarmQueue.includes(path)) previewWarmQueue.push(path);
  }
  if (previewWarmQueue.length && !previewWarmTimer) previewWarmTimer = setTimeout(pumpPreviewWarm, 1200);
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
    {act: `details-file:${id}`, label: 'Details'},
    {act: `download:${id}`, label: 'Download'},
    {act: `copy-file:${id}`, label: 'Copy'},
    {act: `rename-file:${id}`, label: 'Rename'},
    {sep: true},
    {act: `delete-file:${id}`, label: 'Delete'}
  ];
}

function folderMenu(path) {
  const folder = files.find(file => file.folder && file.path === path);
  const photoVisibility = path.startsWith('Photos/')
    ? [{sep: true}, {act: `photo-folder-hidden:${path}`, label: folder?.hidden ? 'Show in Photos' : 'Hide from Photos'}]
    : [];
  const addToPhotos = path !== 'Photos' && !path.startsWith('Photos/')
    ? [{sep: true}, {act: `photos-add-folder:${path}`, label: 'Add folder to Photos'}]
    : [];
  return [
    {act: `send-folder:${path}`, label: 'Share this'},
    {act: `download-folder:${path}`, label: 'Download'},
    {act: `copy-folder:${path}`, label: 'Copy'},
    {act: `details-folder:${path}`, label: 'Details'},
    {act: `upload-here:${path}`, label: 'Upload into'},
    {act: `rename-folder:${path}`, label: 'Rename'},
    ...photoVisibility,
    ...addToPhotos,
    {sep: true},
    {act: `delete-folder:${path}`, label: 'Delete'}
  ];
}

function rootMenu(withSelection = false, withClipboard = false) {
  const items = [
    {act: 'upload', label: 'Upload files…'},
    {act: 'upload-folder-root', label: 'Upload folder…'},
    {act: 'new-folder-root', label: 'New folder'}
  ];
  if (withSelection) items.push({sep: true}, {act: 'batch-move', label: 'Move selected…'}, {act: 'batch-download', label: 'Download selected'}, {act: 'batch-delete', label: 'Delete selected'});
  if (withClipboard) items.push({sep: true}, {act: 'paste', label: 'Paste here'});
  return items;
}

async function moveFile(id, folder) {
  const from = filePath(id);
  const name = from.split('/').pop();
  const to = folder ? `${folder}/${name}` : name;
  if (from === to) return;
  if (!live) { toast('Files can move on the connected node.'); return; }
  try {
    await resolveLibraryOperation(target => engine.renameLibrary(from, target), to);
    state.undo = {kind: 'rename', from: to, to: from};
    await loadLibrary();
    renderMain();
    toast(`Moved ${name} to ${folder || 'the library root'}.`);
  } catch (err) { toast(err.message); }
}

async function resolveLibraryOperation(operation, target) {
  try {
    return await operation(target);
  } catch (err) {
    if (!/conflict|exists/i.test(err.message || '')) throw err;
    if (confirm(`An item already exists at ${target}. Replace it? OK replaces it into Trash; Cancel lets you keep both.`)) {
      await engine.deleteLibrary(target);
      return operation(target);
    }
    const keep = prompt('Keep both. Enter a new destination path, or Cancel to skip.', target);
    if (!keep) throw err;
    return operation(keep);
  }
}

function chooseRange(id) {
  state.photoSelection = null;
  const ids = state.view === 'photos' ? state.photoItems.map(item => item.id) : [...document.querySelectorAll('[data-select-file]')].map(button => button.dataset.selectFile);
  const start = ids.indexOf(state.selectionAnchor || id);
  const end = ids.indexOf(id);
  if (start < 0 || end < 0) return selectFile(id);
  const low = Math.min(start, end), high = Math.max(start, end);
  state.selectedFiles = ids.slice(low, high + 1);
  state.selected = {type: 'file', id};
  renderMain();
  renderDeck();
}

function toggleFileSelection(id) {
  if (state.photoSelection) { clearFileSelection(); toast('Day selection cleared; selecting individual photos.'); }
  const selected = new Set(state.selectedFiles || []);
  if (selected.has(id)) selected.delete(id);
  else selected.add(id);
  state.selectedFiles = [...selected];
  state.selected = selected.size ? {type: 'file', id} : null;
  state.selectionAnchor = id;
  renderMain();
  renderDeck();
}

async function runBatchAction(action) {
  if (state.view === 'photos' && state.photoSelection) {
    const selection = state.photoSelection;
    if (action === 'clear') { clearFileSelection(); renderMain(); renderDeck(); return; }
    if (action === 'delete' && !confirm(`Move ${selection.count} selected photos to Trash?`)) return;
    if (action === 'download' && selection.hidden && !confirm(`Download ${selection.count} selected hidden photos?`)) return;
    const result = await engine.photoSelectionAction({selection_id:selection.id,hidden:selection.hidden,confirm_hidden:selection.hidden,action:action === 'download' ? 'archive' : action});
    if (action === 'download') { await createArchiveJob([],result); return; }
    clearFileSelection(); await loadPhotoPage(true); await loadPhotoAlbums(false); renderMain(); renderDeck(); toast(`${result.deleted} photos moved to Trash.`); return;
  }
  const rows = selectedFileRows();
  if (!rows.length) return;
  if (action === 'clear') { clearFileSelection(); renderMain(); renderDeck(); return; }
  if (!live) { toast('Batch actions are available on the connected node.'); return; }
  if (action === 'download' && rows.length > 1) { await createArchiveJob(rows.map(file => filePath(file.id))); return; }
  if (action === 'delete' && !confirm(`Move ${rows.length} selected file${rows.length === 1 ? '' : 's'} to Trash? They remain recoverable for ${state.trashRetention} days.`)) return;
  let destination = '';
  if (action === 'move') {
    destination = prompt('Move selected files into folder (leave blank for library root)', state.currentPath) ?? '';
    if (destination.startsWith('/') || destination.includes('/.')) { toast('Folder path is not valid.'); return; }
  }
  const failed = [], completed = [];
  for (const file of rows) {
    const from = filePath(file.id);
    try {
      if (action === 'delete') { await engine.deleteLibrary(from); state.undo = {kind: 'restore', path: from}; }
      if (action === 'move') { const to = [destination, file.title].filter(Boolean).join('/'); await resolveLibraryOperation(target => engine.renameLibrary(from, target), to); state.undo = {kind: 'rename', from: to, to: from}; }
      if (action === 'download') await engine.downloadLibrary(from, file.title);
      completed.push(file.id);
    } catch (err) { failed.push(`${file.title}: ${err.message}`); }
  }
  state.selectedFiles = (state.selectedFiles || []).filter(id => !completed.includes(id));
  state.selected = state.selectedFiles.length ? {type: 'file', id: state.selectedFiles[0]} : null;
  if (state.view === 'photos') await loadPhotoPage(true); else await loadLibrary();
  renderMain();
  renderDeck();
  if (failed.length) toast(`${completed.length} completed; ${failed.length} failed. ${failed[0]}`);
  else toast(`${completed.length} file${completed.length === 1 ? '' : 's'} ${action === 'delete' ? 'deleted' : action === 'move' ? 'moved' : 'sent to download'}.`);
}

function setClipboard(paths, mode) {
  state.clipboard = {mode, paths: [...new Set(paths.filter(Boolean))]};
  toast(`${state.clipboard.paths.length} item${state.clipboard.paths.length === 1 ? '' : 's'} ${mode === 'cut' ? 'cut' : 'copied'}. Press Ctrl/Cmd+V in a folder.`);
}

function detailsFile(id) {
  const file = fileByID(id);
  if (!file) return;
  modal(`<span class="eyebrow purple">FILE DETAILS</span><h2>${esc(file.title)}</h2><div class="file-details"><p><strong>Path</strong><span>${esc(filePath(id))}</span></p><p><strong>Type</strong><span>${esc(file.kind || 'FILE')}</span></p><p><strong>Size</strong><span>${esc(file.size || '—')}</span></p><p><strong>Date modified</strong><span>${esc(new Date(file.mtime || 0).toLocaleString())}</span></p></div><div class="dialog-actions"><button class="secondary" data-close>Done</button></div>`);
}

function detailsFolder(path) {
  modal(`<span class="eyebrow purple">FOLDER DETAILS</span><h2>${esc(path.split('/').pop())}</h2><div class="file-details"><p><strong>Path</strong><span>${esc(path)}</span></p></div><div class="dialog-actions"><button class="secondary" data-close>Done</button></div>`);
}

async function pasteClipboard() {
  const clip = state.clipboard || {paths: [], mode: ''};
  if (!live || !clip.paths.length) return;
  const failed = [];
  for (const source of clip.paths) {
    const target = [state.currentPath, source.split('/').pop()].filter(Boolean).join('/');
    try {
      if (clip.mode === 'cut') await resolveLibraryOperation(destination => engine.renameLibrary(source, destination), target);
      else await resolveLibraryOperation(destination => engine.copyLibrary(source, destination), target);
    } catch (err) {
      failed.push(`${source}: ${err.message}`);
    }
  }
  if (clip.mode === 'cut' && !failed.length) state.clipboard = {mode: '', paths: []};
  await loadLibrary();
  renderMain();
  renderDeck();
  toast(failed.length ? `${failed.length} paste item${failed.length === 1 ? '' : 's'} failed: ${failed[0]}` : 'Pasted into this folder.');
}

async function renameSelected() {
  const folder = state.selected?.type === 'folder' ? state.selected.path : '';
  const rows = selectedFileRows();
  const source = folder || (rows.length === 1 ? filePath(rows[0].id) : '');
  if (!source || !live) return;
  const next = prompt('Rename to', source.split('/').pop());
  if (!next || next.includes('/')) return;
  try {
    const target = source.split('/').slice(0, -1).concat(next).join('/');
    await resolveLibraryOperation(destination => engine.renameLibrary(source, destination), target);
    await loadLibrary();
    renderMain();
    toast('Renamed.');
  } catch (err) { toast(`Rename failed: ${err.message}. Choose a different name if it already exists.`); }
}

async function deleteSelectedFolder() {
  const path = state.selected?.type === 'folder' ? state.selected.path : '';
  if (!path || !live || !confirm(`Delete ${path}? It will stay in Trash for ${state.trashRetention} days.`)) return;
  try {
    await engine.deleteLibrary(path);
    state.undo = {kind: 'restore', path};
    state.selected = null;
    await loadLibrary();
    renderMain();
    renderDeck();
    toast('Moved to Trash.');
  } catch (err) { toast(err.message); }
}

async function undoLast() {
  const action = state.undo;
  if (!action || !live) return;
  try {
    if (action.kind === 'restore') await engine.restoreTrash(action.path);
    else await engine.renameLibrary(action.from, action.to);
    state.undo = null;
    await loadLibrary();
    if (state.view === 'trash') await loadTrash();
    renderMain();
    renderDeck();
    toast('Undone.');
  } catch (err) { toast(`Undo failed: ${err.message}. A later change may have caused a conflict.`); }
}

async function createArchiveJob(paths, preparedJob = null) {
  try {
    const job = preparedJob || await engine.createArchive(paths);
    state.archiveJobs.unshift(job);
    renderUploadTray();
    for (let attempt = 0; attempt < 120; attempt++) {
      await new Promise(resolve => setTimeout(resolve, 500));
      const current = await engine.archiveStatus(job.id);
      const index = state.archiveJobs.findIndex(item => item.id === job.id);
      if (index >= 0) state.archiveJobs[index] = current;
      renderUploadTray();
      if (current.status === 'ready') {
        engine.downloadArchive(current.id);
        toast(`ZIP ready: ${current.files} files.`);
        return;
      }
      if (current.status === 'failed' || current.status === 'cancelled') {
        toast(current.error || 'ZIP preparation failed.');
        return;
      }
    }
    toast('ZIP preparation is still running in the background.');
  } catch (err) { toast(err.message); }
}

async function previewFile(id) {
  const f = files.find(x => x.id === id) || state.photoItems?.find(x => x.id === id) || state.photoSelectedItems?.get(id); if (!f || f.folder) return;
  const path = filePath(id);
  if (!live) { toast('Preview is available when the node is connected.'); return; }
  if (f.entryID && /\.(?:jpe?g|png|gif|webp|heic|heif|avif|tiff?)$/i.test(f.title)) {
    const response = await fetch(`/api/library/thumbnail?id=${encodeURIComponent(f.entryID)}&size=1280`);
    if (!response.ok) { toast('Photo preview unavailable.'); return; }
    const url = URL.createObjectURL(await response.blob());
    modal(`<span class="eyebrow purple">PHOTO PREVIEW</span><h2>${esc(f.title)}</h2><img class="file-preview-image" src="${url}" alt="${esc(f.title)}"><div class="preview-actions"><a class="secondary button-link" href="/api/library?path=${encodeURIComponent(path)}" download="${esc(f.title)}">Download original</a></div><p class="eyebrow">${esc(path)}</p>`, true);
    $('#modal').addEventListener('close', () => URL.revokeObjectURL(url), {once: true});
    return;
  }
  const previewable = new Set(['IMG', 'JPG', 'JPEG', 'PNG', 'GIF', 'WEB', 'WEBP', 'SVG', 'PDF', 'TXT', 'MD', 'CSV', 'JSON', 'DOC', 'DOCX', 'XLS', 'XLSX', 'PPT', 'PPTX', 'ODT', 'ODS', 'ODP', 'STL', '3MF', 'MP3', 'WAV', 'FLAC', 'M4A', 'AAC', 'OGG', 'OGA', 'OPUS', 'FLA', 'MP4', 'MOV', 'WEBM', 'MKV', 'AVI', 'M4V']);
  if (!previewable.has(String(f.kind).toUpperCase())) { toast('This file opens as a download.'); return; }
  const mediaKind = new Set(['MP3', 'WAV', 'FLAC', 'M4A', 'AAC', 'OGG', 'OGA', 'OPUS', 'MP4', 'MOV', 'WEBM', 'MKV', 'AVI', 'M4V']);
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
    if (path.toLowerCase().endsWith('.svg')) body = `<pre class="file-preview-text">${esc(await result.blob.text())}</pre>`;
    else if (type.startsWith('image/')) body = `<img class="file-preview-image" src="${url}" alt="${esc(f.title)}">`;
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

let photoViewerIndex = -1;
let photoViewerHistoryPushed = false;
let photoRouteViewerID = '';
let photoViewerReturnHash = '#photos';
function openPhotoViewer(id, pushHistory = true) {
  const index = state.photoItems.findIndex(item => item.id === id);
  if (index < 0) return previewFile(id);
  if (pushHistory && !photoViewerHistoryPushed) {
    const returnHash = location.hash || '#photos';
    history.pushState({photoViewer: true, photoID: id, returnHash}, '', '#photos/view/' + encodeURIComponent(id));
    photoViewerHistoryPushed = true;
  }
  photoViewerIndex = index;
  state.selected = {type: 'file', id};
  state.selectedFiles = [id];
  state.selectionAnchor = id;
  state.photoViewerInfo = false;
  state.photoZoom = 1;
  renderPhotoViewer();
  const hiddenMode = state.photosMode === 'hidden';
  engine.photoDetail(id, hiddenMode).then(detail => {
    const current = state.photoItems[photoViewerIndex];
    if (!current || current.id !== id) return;
    Object.assign(current, engine.toFixture(detail));
    renderPhotoViewer();
  }).catch(() => {});
}

function closePhotoViewer() {
  if (photoViewerHistoryPushed) {
    history.back();
    return;
  }
  $('#modal').close();
  if (location.hash.startsWith('#photos/view/')) history.replaceState({}, '', photoViewerReturnHash || '#photos');
}

async function showPhotoInLibrary() {
  const item = state.photoItems[photoViewerIndex];
  if (!item?.path) return;
  const parent = item.path.includes('/') ? item.path.slice(0, item.path.lastIndexOf('/')) : '';
  photoViewerHistoryPushed = false;
  $('#modal').close();
  setLibraryPath(parent, false);
  history.replaceState({}, '', routeHash('library'));
}

async function restorePhotoViewerRoute(id) {
  if (!id || state.view !== 'photos') return;
  let item = state.photoItems.find(row => row.id === id);
  if (!item) {
    try {
      const detail = await engine.photoDetail(id, state.photosMode === 'hidden');
      item = engine.toFixture(detail);
      state.photoItems = [item];
      state.photoCursor = '';
      state.photoHasMore = false;
    } catch (err) {
      if (location.hash.startsWith('#photos/view/')) history.replaceState({}, '', photoViewerReturnHash || '#photos');
      photoRouteViewerID = '';
      toast('That photo is no longer available. Returning to Photos.');
      return;
    }
  }
  openPhotoViewer(id, false);
}

function renderPhotoViewer() {
  const item = state.photoItems[photoViewerIndex];
  if (!item) { $('#modal').close(); return; }
  const path = item.path || filePath(item.id);
  const video = /\.(?:mp4|mov|m4v|webm|mkv)$/i.test(path);
  const hiddenParam = state.photosMode === 'hidden' ? '&hidden=1' : '';
  const date = item.captureTime ? modifiedLabel(item.captureTime) : 'Date unknown';
  const source = video
    ? `<video class="photo-viewer-media" src="/api/v1/photos/assets/${encodeURIComponent(item.entryID || item.id)}/original${state.photosMode === 'hidden' ? '?hidden=1' : ''}" controls autoplay playsinline preload="metadata"></video>`
    : `<img class="photo-viewer-media" style="--photo-zoom:${state.photoZoom || 1}" src="/api/v1/photos/assets/${encodeURIComponent(item.entryID || item.id)}/thumbnail?size=1280${hiddenParam}" alt="${esc(item.title)}">`;
  const caption = item.caption ? `<p>${esc(item.caption)}</p>` : '';
  $('#modal').classList.add('photo-viewer');
  $('#modal').classList.remove('wide');
  $('#modal-content').innerHTML = `<div class="photo-viewer-shell" data-photo-viewer-stage>
    <header class="photo-viewer-top"><div><strong>${esc(item.title)}</strong><small>${photoViewerIndex + 1} of ${state.photoItems.length}</small></div>
      <div class="photo-viewer-actions"><button class="secondary" data-action="photo-show-library">Show in Library</button><button class="secondary" data-action="photo-add-to-album">Add to album</button><button class="secondary" data-action="photo-edit">Edit details</button><button class="secondary" data-action="photo-toggle-archive">${item.archived ? 'Unarchive' : 'Archive'}</button><button class="secondary" data-action="photo-toggle-favorite" aria-pressed="${item.favorite ? 'true' : 'false'}">${item.favorite ? '★ Favorite' : '☆ Favorite'}</button><button class="secondary" data-action="photo-zoom-out" aria-label="Zoom out">−</button><span class="photo-viewer-zoom">${Math.round((state.photoZoom || 1) * 100)}%</span><button class="secondary" data-action="photo-zoom-in" aria-label="Zoom in">+</button><button class="secondary" data-action="photo-viewer-info" aria-expanded="${state.photoViewerInfo ? 'true' : 'false'}">Details</button><a class="secondary button-link" href="/api/v1/photos/assets/${encodeURIComponent(item.entryID || item.id)}/original?download=1${state.photosMode === 'hidden' ? '&hidden=1' : ''}" download="${esc(item.title)}">Download original</a><button class="icon-button" data-action="photo-viewer-close" aria-label="Close viewer">×</button></div>
    </header>
    <button class="photo-viewer-nav previous" data-action="photo-viewer-prev" aria-label="Previous photo" ${photoViewerIndex === 0 ? 'disabled' : ''}>‹</button>
    <div class="photo-viewer-stage">${source}</div>
    <button class="photo-viewer-nav next" data-action="photo-viewer-next" aria-label="Next photo" ${photoViewerIndex >= state.photoItems.length - 1 && !state.photoHasMore ? 'disabled' : ''}>›</button>
    <footer class="photo-viewer-caption"><span>${esc(date)}</span>${caption}</footer>
    <aside class="photo-viewer-info" ${state.photoViewerInfo ? '' : 'hidden'}><strong>Details</strong><dl><dt>Capture date</dt><dd>${esc(date)}</dd><dt>File</dt><dd>${esc(path)}</dd><dt>Size</dt><dd>${esc(item.size)}</dd>${item.captureSource ? `<dt>Date source</dt><dd>${esc(item.captureSource)}</dd>` : ''}</dl></aside>
  </div>`;
  if (!$('#modal').open) $('#modal').showModal();
  const stage = $('#modal [data-photo-viewer-stage]');
  let touchStart = null;
  stage?.addEventListener('pointerdown', event => { touchStart = event.clientX; });
  stage?.addEventListener('pointerup', event => {
    if (touchStart === null) return;
    const delta = event.clientX - touchStart;
    touchStart = null;
    if (Math.abs(delta) > 70) movePhotoViewer(delta < 0 ? 1 : -1);
  });
  prefetchPhotoViewerNeighbors();
}

function editPhotoDetails() {
  const item = state.photoItems[photoViewerIndex];
  if (!item) return;
  const captured = item.captureTime ? item.captureTime.slice(0,16) : '';
  const offset = item.captureOffsetMinutes === undefined || item.captureOffsetMinutes === null ? '' : item.captureOffsetMinutes === 0 ? '+00:00' : `${item.captureOffsetMinutes < 0 ? '-' : '+'}${String(Math.floor(Math.abs(item.captureOffsetMinutes)/60)).padStart(2,'0')}:${String(Math.abs(item.captureOffsetMinutes)%60).padStart(2,'0')}`;
  modal(`<span class="eyebrow purple">PHOTO / DETAILS</span><h2>Edit details</h2><form id="photo-edit-form" class="auth-form"><label>Caption<textarea name="caption" maxlength="4096">${esc(item.caption || '')}</textarea></label><label>Capture date and time<input name="captured_at" type="datetime-local" value="${esc(captured)}"></label><label>UTC offset (leave blank if unknown)<input name="capture_offset" value="${esc(offset)}" placeholder="+00:00" pattern="[+-][0-9]{2}:[0-9]{2}"></label><label>Rotation<select name="rotation">${[0,90,180,270].map(rotation=>`<option value="${rotation}" ${rotation === (item.userRotation || 0) ? 'selected' : ''}>${rotation}°</option>`).join('')}</select></label><p class="eyebrow">Changing the date affects Photos ordering. The original file stays unchanged.</p><div class="dialog-actions"><button class="primary">Save details</button><button class="secondary" type="button" data-action="photo-edit-cancel">Cancel</button></div></form>`, true);
}

function photoAlbumByID(id) { return (state.photoAlbums || []).find(album => album.id === id && album.source === 'custom'); }
function editPhotoAlbum(id = '', initialIDs = state.selectedFiles || [], selection = state.photoSelection) {
  const album = id ? photoAlbumByID(id) : null;
  if (id && !album) return;
  photoAlbumPendingIDs = [...new Set(initialIDs)].filter(Boolean);
  photoAlbumPendingSelection = id ? null : selection;
  modal(`<span class="eyebrow purple">PHOTOS / ALBUM</span><h2>${album ? 'Edit album' : 'New album'}</h2><form id="photo-album-form" class="auth-form"><input type="hidden" name="id" value="${esc(album?.id || '')}"><input type="hidden" name="revision" value="${album?.revision || 0}"><label>Title<input name="title" required maxlength="200" value="${esc(album?.title || '')}"></label><label>Description<textarea name="description" maxlength="4000">${esc(album?.description || '')}</textarea></label><label>Album order<input name="position" type="number" min="0" max="100000" value="${album?.position || 0}"></label>${album ? `<label>Cover<select name="cover_id">${[...new Set([album.cover_id, ...(album.asset_ids || []).slice(0,100)].filter(Boolean))].map((id,index)=>`<option value="${esc(id)}" ${id === album.cover_id ? 'selected' : ''}>${esc(fileByID(id)?.title || `Photo ${index+1}`)}</option>`).join('')}</select></label>` : ''}<p class="eyebrow">${album ? `${album.count} visible photos in this album.` : `${photoAlbumPendingSelection?.count || initialIDs.length} selected photos will be added.`}</p><div class="dialog-actions"><button class="primary">Save album</button><button class="secondary" type="button" data-action="photo-edit-cancel">Cancel</button></div></form>`, true);
}

let photoAlbumPendingIDs = [];
let photoAlbumPendingSelection = null;
function addPhotosToAlbum(ids, selection = null) {
  photoAlbumPendingSelection = selection;
  photoAlbumPendingIDs = [...new Set(ids || [])].filter(Boolean);
  const albums = (state.photoAlbums || []).filter(album => album.source === 'custom');
  if (!photoAlbumPendingIDs.length && !selection?.count) { toast('Select at least one photo.'); return; }
  if (!albums.length) { editPhotoAlbum('', photoAlbumPendingIDs, selection); return; }
  modal(`<span class="eyebrow purple">PHOTOS / ORGANIZE</span><h2>Add to album</h2><form id="photo-album-members-form" class="auth-form">${albums.map(album => `<label class="choice"><input type="radio" name="album_id" value="${esc(album.id)}" required><span><strong>${esc(album.title)}</strong><small>${album.count} visible items</small></span></label>`).join('')}<div class="dialog-actions"><button class="primary">Add ${selection?.count || photoAlbumPendingIDs.length} ${(selection?.count || photoAlbumPendingIDs.length) === 1 ? 'photo' : 'photos'}</button><button class="secondary" type="button" data-action="photo-edit-cancel">Cancel</button></div></form>`, true);
}


let photoDuplicateCursor='';
async function reviewPhotoDuplicates(cursor='') {
  if (!live || !state.unlocked) return;
  const username=state.username, hidden=state.photosMode==='hidden';
  const page=await engine.listPhotoDuplicates(cursor,hidden);
  if (!state.unlocked || state.username!==username || hidden!==(state.photosMode==='hidden')) return;
  photoDuplicateCursor=page.next_cursor || '';
  modal(`<h2>Exact duplicates</h2><p>Choose a preferred copy for this review. Originals and album membership stay intact.</p>${(page.groups || []).map(group=>`<section class="duplicate-group"><h3>${group.count} copies</h3>${group.items.map(item=>`<div class="file-row"><span class="file-name"><strong>${esc(item.path)}</strong><small>${formatBytes(item.size)}${item.preferred_photo?' · preferred':''}</small></span><button class="text-button" data-photo-prefer="${esc(item.id)}" ${item.preferred_photo?'disabled':''}>Prefer this copy</button></div>`).join('')}${group.has_more_items?'<p>Showing the first 200 copies. All originals remain in Library.</p>':''}</section>`).join('') || '<p>No exact duplicates in this view.</p>'}<div class="dialog-actions">${photoDuplicateCursor?'<button class="secondary" data-action="photo-duplicates-more">More groups</button>':''}<button class="secondary" type="button" data-close>Done</button></div>`);
}
let photoGrabIDs = [];
let photoGrabSelection = null;
function sharePhotos(ids = state.selectedFiles || [], title = 'Photos', selection = state.photoSelection) {
  photoGrabSelection = selection;
  photoGrabIDs = [...new Set(ids)].filter(Boolean);
  if (!photoGrabIDs.length && !selection?.count) { toast('Select photos to share.'); return; }
  const hidden = selection?.hidden ?? (state.photosMode === 'hidden');
  modal(`<h2>Share photos</h2><form id="photo-grab-form" class="auth-form"><label>Title<input name="title" maxlength="200" value="${esc(title)}" required></label><label>Passphrase (optional)<input name="passphrase" type="password" autocomplete="new-password"></label><label>Expires<select name="expiry"><option value="24h">24 hours</option><option value="3d">3 days</option><option value="7d">7 days</option></select></label><label>Download transfers<input name="grabs" type="number" min="1" max="10000" value="3" required></label>${hidden ? '<label class="choice"><input name="confirm_hidden" type="checkbox" required>Share these selected hidden photos with anyone who has this grab link.</label>' : ''}<p>${selection?.count || photoGrabIDs.length} selected photos. The link freezes this selection. Browsing uses no retries; each requested download uses one.</p><p>Original downloads may contain embedded location metadata.</p><div class="dialog-actions"><button class="primary">Create grab</button><button class="secondary" type="button" data-close>Cancel</button></div></form>`, true);
}
async function sharePhotoAlbum(path) {
  const album = (state.photoAlbums || []).find(item=>item.path === path);
  if (!album) return;
  const username = state.username;
  const selection = await engine.createPhotoSelection({filter:{album:path},mode:'all'});
  if (!state.unlocked || state.username !== username) return;
  sharePhotos([],album.title,selection);
}

function prefetchPhotoViewerNeighbors() {
  for (const item of [state.photoItems[photoViewerIndex - 1], state.photoItems[photoViewerIndex + 1]]) {
    if (item && !/\.(?:mp4|mov|m4v|webm|mkv)$/i.test(item.path || '')) {
      const image = new Image();
      image.src = `/api/v1/photos/assets/${encodeURIComponent(item.entryID || item.id)}/thumbnail?size=1280${state.photosMode === 'hidden' ? '&hidden=1' : ''}`;
    }
  }
}

function movePhotoViewer(delta) {
  const next = photoViewerIndex + delta;
  if (next < 0) {
    if (state.photoPreviousCursor && !state.photoLoading) {
      const before = state.photoItems.length;
      loadPhotoPage(false, false, 'before').then(() => {
        const added = state.photoItems.length - before;
        if (added > 0) { photoViewerIndex = Math.max(0, added - 1); renderPhotoViewer(); }
      });
    }
    return;
  }
  if (next >= state.photoItems.length) {
    if (state.photoHasMore && !state.photoLoading) loadPhotoPage(false, false).then(() => {
      if (photoViewerIndex + delta < state.photoItems.length) { photoViewerIndex += delta; renderPhotoViewer(); }
    });
    return;
  }
  photoViewerIndex = next;
  state.selected = {type: 'file', id: state.photoItems[next].id};
  state.selectedFiles = [state.photoItems[next].id];
  state.selectionAnchor = state.photoItems[next].id;
  renderPhotoViewer();
}

function newUploadID() {
  return globalThis.crypto?.randomUUID?.() || `upload-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function uploadState() {
  if (!state.upload.items) state.upload.items = [];
  if (!state.upload.rails?.length) state.upload.rails = Array.from({length: UPLOAD_RAILS}, () => ({name: 'Waiting…', pct: 0, status: 'waiting'}));
  return state.upload;
}

async function setUploadStorageUser(username) {
	const nextKey = username ? `wzcl-upload-queue:${username}` : '';
	const restoreKey = nextKey;
	if (nextKey !== uploadStorageKey) state.upload.items = [];
	uploadStorageKey = nextKey;
	if (!uploadStorageKey) return;
	try {
		const saved = JSON.parse(localStorage.getItem(uploadStorageKey) || '[]');
		state.upload.items = Array.isArray(saved) ? saved.map(item => ({...item, file: null, status: item.status === 'done' ? 'done' : 'needs-file', error: item.status === 'done' ? '' : 'Select this file again to resume.'})) : [];
		const serverSessions = await engine.listUploads();
		if (uploadStorageKey !== restoreKey || state.username !== username || !state.unlocked) return;
		for (const session of serverSessions) {
			if (state.upload.items.some(item => item.sessionId === session.id)) continue;
			state.upload.items.push({id: newUploadID(), sessionId: session.id, target: session.path, size: session.size, loaded: session.offset, confirmed: session.offset, status: session.status === 'complete' ? 'done' : 'needs-file', error: session.status === 'complete' ? '' : 'Select this file again to resume.', attempts: 0});
		}
		refreshUploadSummary();
	} catch {}
}

async function validateReselectedUpload(file, session) {
	if (file.size !== session.size) return false;
	for (let i = 0; i < (session.chunk_hashes || []).length; i++) {
		const start = i * UPLOAD_CHUNK_SIZE;
		const end = Math.min(file.size, start + UPLOAD_CHUNK_SIZE);
		if (end <= start || await engine.sha256Hex(file.slice(start, end)) !== session.chunk_hashes[i]) return false;
	}
	return true;
}

function persistUploadQueue() {
  if (!uploadStorageKey) return;
  clearTimeout(uploadPersistTimer);
  uploadPersistTimer = setTimeout(() => {
    const safe = uploadState().items.map(({file, ...item}) => item);
    try { localStorage.setItem(uploadStorageKey, JSON.stringify(safe)); } catch {}
  }, 80);
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
  persistUploadQueue();
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

const waitForUploadRetry = delay => new Promise(resolve => setTimeout(resolve, delay));

async function appendChunkWithRetry(item, session, offset, chunk, slot) {
	const hash = await engine.sha256Hex(chunk);
	for (let attempt = 0; attempt < 5; attempt++) {
		const request = engine.appendUpload(session.id, offset, chunk, hash, (sent, _total, phase) => {
			item.loaded = sent;
			const pct = item.size ? (sent / item.size) * 100 : 100;
			item.status = 'uploading';
			state.upload.rails[slot] = {name: item.target, pct, status: phase || `${Math.round(pct)}%`};
			refreshUploadSummary();
		});
		uploadRequests.set(item.id, request);
		try {
			return await request;
		} catch (err) {
			uploadRequests.delete(item.id);
			request.abort?.();
			item.loaded = offset;
			if (attempt === 4) throw err;
			await waitForUploadRetry(400 * (2 ** attempt));
			const current = await engine.uploadStatus(session.id);
			if (current.path !== item.target || current.size !== item.size) throw new Error('Saved upload no longer matches this file.');
			if (current.offset === offset + chunk.size) return current;
			if (current.offset !== offset) throw new Error(`Upload resume position changed to ${current.offset}. Retry the upload.`);
		}
	}
}

async function uploadWorker(slot) {
  while (true) {
    const item = uploadItemForSlot(slot);
    if (!item) return;
    try {
      let session = item.sessionId ? await engine.uploadStatus(item.sessionId).catch(() => null) : null;
      if (session && (session.path !== item.target || session.size !== item.size)) session = null;
      if (!session) session = await engine.createUpload(item.target, item.size);
			if (session.offset && !await validateReselectedUpload(item.file, session)) {
				const mismatch = new Error('The selected file does not match the saved upload. Select the original file to resume.');
				mismatch.needsFile = true;
				throw mismatch;
			}
      item.sessionId = session.id;
      item.loaded = session.offset || 0;
      item.confirmed = item.loaded;
      while (item.loaded < item.size) {
        const offset = item.loaded;
        const chunk = item.file.slice(offset, Math.min(item.size, offset + UPLOAD_CHUNK_SIZE));
				const result = await appendChunkWithRetry(item, session, offset, chunk, slot);
        item.loaded = result.offset;
        item.confirmed = result.offset;
        session = result;
        refreshUploadSummary();
      }
      item.status = 'saving';
      state.upload.rails[slot] = {name: item.target, pct: 100, status: 'saving'};
      refreshUploadSummary();
      await engine.finalizeUpload(session.id);
      item.loaded = item.size;
      item.confirmed = item.size;
      item.status = 'done';
      item.error = '';
      state.upload.rails[slot] = {name: `✓ ${item.target}`, pct: 100, status: 'done'};
      schedulePreviewWarm([item.target]);
      scheduleUploadRefresh();
    } catch (err) {
      if (Number.isFinite(err.offset)) {
        item.loaded = err.offset;
        item.confirmed = err.offset;
      }
      if (item.status === 'cancelled') {
        state.upload.rails[slot] = {name: `× ${item.target}`, pct: item.size ? (item.loaded / item.size) * 100 : 0, status: 'cancelled'};
		} else {
			item.status = err.needsFile ? 'needs-file' : 'failed';
			item.error = err.message;
			state.upload.rails[slot] = {name: `× ${item.target}`, pct: item.size ? (item.loaded / item.size) * 100 : 0, status: item.status};
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
  filesToUpload.forEach(item => {
    const file = item.file;
    const relative = item.relative || file.name;
    const target = [prefix, relative].filter(Boolean).join('/');
    const pending = upload.items.find(candidate => candidate.status === 'needs-file' && candidate.target === target && candidate.size === file.size);
    if (pending) {
      pending.file = file;
      pending.status = 'queued';
      pending.error = '';
      return;
    }
    upload.items.push({id: newUploadID(), file, relative, target, size: file.size, loaded: 0, confirmed: 0, status: 'queued', attempts: 0, error: ''});
  });
  refreshUploadSummary();
  runUploadQueue();
}

function retryFailedUploads() {
  const upload = uploadState();
  upload.items.filter(item => item.status === 'failed').forEach(item => { item.status = 'queued'; item.loaded = item.confirmed || 0; item.error = ''; item.attempts = (item.attempts || 0) + 1; });
  upload.dismissed = false;
  refreshUploadSummary();
  runUploadQueue();
}

function cancelUploads() {
  const upload = uploadState();
  upload.items.filter(item => item.status === 'queued').forEach(item => { item.status = 'cancelled'; });
  uploadRequests.forEach((request, id) => {
    const item = upload.items.find(candidate => candidate.id === id);
    if (item) { item.status = 'cancelled'; request.abort?.(); if (item.sessionId) engine.cancelUpload(item.sessionId).catch(() => {}); }
  });
  upload.items.filter(item => item.status === 'queued' && item.sessionId).forEach(item => engine.cancelUpload(item.sessionId).catch(() => {}));
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
  $('#modal').classList.remove('photo-viewer');
  $('#modal-content').innerHTML = html;
  $('#modal').showModal();
}

function help() {
  modal('<span class="eyebrow purple">KEEP YOUR HANDS ON THE KEYS</span><h2>The short route.</h2>' +
    [['Home / Library / Send / Capsules / Places', '1–5'], ['Mint grab link', 'S'], ['Undo last move/delete', 'Ctrl/Cmd+Z'], ['Copy / cut / paste', 'Ctrl/Cmd+C/X/V'], ['Lock vault', 'L'], ['File / folder actions', 'Right-click or ⋯'], ['This cheat sheet', '?']].map(([a, b]) => `<div class="shortcut"><span>${a}</span><kbd>${b}</kbd></div>`).join(''));
}

function vaultCard() {
  modal(`<img class="auth-brand" src="weazlcloud.png" alt="WeazlCloud"><span class="eyebrow purple">ACCOUNT SETTINGS</span><h2>${esc(state.fullName || state.username || 'Your account')}</h2><p>Local identity and vault controls. The administrator cannot open this user's vault.</p><form id="settings-form" class="auth-form"><label>Full name<input name="full_name" value="${esc(state.fullName)}" maxlength="120" placeholder="Your full name"></label><label>Current account password<input name="current_password" type="password" autocomplete="current-password"></label><label>New account password<input name="new_password" type="password" autocomplete="new-password"></label><button class="primary">Save settings</button></form><hr><h3>Rekey vault</h3><p class="eyebrow">Changes the vault passphrase while preserving the library.</p><form id="rekey-form" class="auth-form"><label>Current vault passphrase<input name="current" type="password" required></label><label>New vault passphrase<input name="next" type="password" required></label><label>Confirm new vault passphrase<input name="confirm" type="password" required></label><button class="secondary">Rekey vault</button></form><div class="dialog-actions"><button class="secondary" id="do-lock">${state.unlocked ? 'Lock vault' : 'Unlock…'}</button></div>` , true);
}

function routeHash(view = state.view) {
  if (view === 'photos') {
    if (state.photoQuery) return state.photosMode === 'hidden' ? '#photos/hidden/search/' + encodeURIComponent(state.photoQuery) : '#photos/search/' + encodeURIComponent(state.photoQuery);
    if (state.photosAlbum) return '#photos/album/' + encodeURIComponent(state.photosAlbum);
    if (state.photoDate) return '#photos/date/' + encodeURIComponent(state.photoDate);
    return state.photosMode === 'all' ? '#photos' : '#photos/' + encodeURIComponent(state.photosMode);
  }
  return `#${view}${view === 'library' && state.currentPath ? '/' + encodeURIComponent(state.currentPath) : ''}`;
}

function setLibraryPath(path, push = true) {
  stopMediaPlayback();
  clearFileSelection();
  state.currentPath = String(path || '').replace(/^\/+|\/+$/g, '');
  state.view = 'library';
  renderMain();
  renderDeck();
  if (push) history.pushState({}, '', routeHash('library'));
  if (live && state.unlocked) loadLibrary(true).catch(err => toast(err.message));
}

function applyRoute() {
  if ($('#modal').classList.contains('photo-viewer') && !history.state?.photoViewer) {
    photoViewerHistoryPushed = false;
    $('#modal').close();
    return;
  }
  const raw = location.hash.replace(/^#/, '') || 'home';
  const routeParts = raw.split('/');
  const photoRouteViewer = routeParts[0] === 'photos' && routeParts[1] === 'view';
  photoViewerReturnHash = String(history.state?.returnHash || '#photos');
  const sourceRoute = photoRouteViewer ? String(history.state?.returnHash || '#photos').replace(/^#/, '') : raw;
  const [view, encoded] = sourceRoute.split('/');
  const allowed = new Set(['home', 'library', 'photos', 'send', 'capsules', 'places', 'admin', 'kit', 'takeout', 'check', 'destroy', 'trash']);
  const previous=state.view, switching=previous !== (allowed.has(view)?view:'home');
  if(switching){
    const anchor=previous==='photos'?[...document.querySelectorAll('.photo-grid [data-select-file]')].map(el=>({id:el.dataset.selectFile,top:el.getBoundingClientRect().top,date:state.photoItems.find(item=>item.id===el.dataset.selectFile)?.captureTime?.slice(0,10)||'unknown'})).find(row=>row.top>=0&&row.top<window.innerHeight):null;
    modeMemory.remember(previous,state,window.scrollY,anchor);stopMediaPlayback();
    if(previous==='photos'){photoPageController?.abort();photoPageRequest++;state.photoLoading=false;photoSelectionRequest++;}
  }
  state.view = allowed.has(view) ? view : 'home';
  if (state.view !== 'library' && libraryLoadController) {
    libraryPageRequest++;
    libraryLoadController.abort();
    libraryLoadController = null;
  }
  if (state.view === 'library') {
    try { state.currentPath = encoded ? decodeURIComponent(encoded) : ''; } catch { state.currentPath = ''; }
  }
  if (state.view === 'photos') {
    const parts = sourceRoute.split('/');
    const hiddenSearch = encoded === 'hidden' && parts[2] === 'search';
    const regularSearch = encoded === 'search';
    state.photosMode = hiddenSearch ? 'hidden' : regularSearch ? 'all' : ['albums', 'favorites', 'recent', 'hidden', 'archived'].includes(encoded) || encoded === 'album' ? (encoded === 'album' ? 'albums' : encoded) : 'all';
    try { state.photoQuery = hiddenSearch ? decodeURIComponent(parts[3] || '') : regularSearch ? decodeURIComponent(parts[2] || '') : ''; } catch { state.photoQuery = ''; }
    try { state.photosAlbum = encoded === 'album' ? decodeURIComponent(parts[2] || '') : ''; } catch { state.photosAlbum = ''; }
    try { state.photoDate = encoded === 'date' ? decodeURIComponent(parts[2] || '') : ''; } catch { state.photoDate = ''; }
    const navigation=history.state?.photoNavigation?.owner===state.username?history.state.photoNavigation:null;
    if(navigation?.scope){const s=navigation.scope;state.photoCamera=s.camera||'';state.photoSearchType=s.type||'';state.photoFrom=s.from||'';state.photoTo=s.to||'';state.photoOutsideAlbums=Boolean(s.outside_albums);state.photoQuery=s.q||'';}
    state.photosShown = 60;
    state.photoItems = [];
    state.photoCursor = '';
    state.photoPreviousCursor = '';
    loadPhotoAlbums();
    loadPhotoDates();
    loadPhotoPreparation();
    try { photoRouteViewerID = photoRouteViewer ? decodeURIComponent(routeParts[2] || '') : ''; } catch { photoRouteViewerID = ''; }
    const restored=switching?modeMemory.restore('photos',state):null;
    if(switching&&!restored)clearFileSelection();
    state.photoAnchor = photoRouteViewerID || navigation?.anchor || restored?.anchor?.id || '';
    state.photoFallbackDate=navigation?.date || restored?.anchor?.date || '';
    if(navigation?.anchor || restored?.anchor?.id){state.photoJumpAnchor=navigation?.anchor || restored.anchor.id;state.photoJumpOffset=navigation?.offset??restored?.anchor?.top??145;}
    loadPhotoPage(true).then(() => {
      state.photoModeDirty=false;
      if(restored)requestAnimationFrame(()=>{const tile=[...document.querySelectorAll('.photo-grid [data-select-file]')].find(el=>el.dataset.selectFile===restored.anchor?.id);if(tile)window.scrollBy({top:tile.getBoundingClientRect().top-restored.anchor.top,behavior:'auto'});else if(!restored.anchor)window.scrollTo({top:restored.scroll,behavior:'auto'});schedulePhotoGridWindow();});
      if (photoRouteViewerID) restorePhotoViewerRoute(photoRouteViewerID);
    }).catch(err => toast(err.message));
  }
  if (state.view === 'library' && live && state.unlocked) {
    const restored=switching?modeMemory.restore('library',state):null;
    if(switching&&!restored)clearFileSelection();
    loadLibrary(true).then(()=>{if(restored&&state.view==='library')requestAnimationFrame(()=>window.scrollTo({top:restored.scroll,behavior:'auto'}));}).catch(err => toast(err.message));
  }
  renderMain();
  renderDeck();
  if (state.view === 'trash') loadTrash();
  if (state.view === 'takeout') refreshTakeout();
}

function navigate(view, replace = false) {
  const previous = state.view, switching = previous !== view;
  if (switching) {
    const anchor = previous === 'photos' ? [...document.querySelectorAll('.photo-grid [data-select-file]')].map(el=>({id:el.dataset.selectFile,top:el.getBoundingClientRect().top,date:state.photoItems.find(item=>item.id===el.dataset.selectFile)?.captureTime?.slice(0,10)||'unknown'})).find(row=>row.top>=0&&row.top<window.innerHeight) : null;
    modeMemory.remember(previous,state,window.scrollY,anchor);
  }
  if (previous === 'photos' && view !== 'photos') { photoPageController?.abort(); photoPageRequest++; state.photoLoading=false; photoSelectionRequest++; }
  if (view === 'trash' && previous !== 'trash') {
    state.trashScope=previous === 'photos' ? 'photos' : 'library'; state.trashHidden=state.photosMode==='hidden'; state.trash=[];
  }
  if (switching) stopMediaPlayback();
  if (view !== 'library' && libraryLoadController) { libraryPageRequest++; libraryLoadController.abort(); libraryLoadController=null; }
  state.view=view;
  const restored=switching ? modeMemory.restore(view,state) : null;
  if (switching && ['library','photos'].includes(view) && !restored) clearFileSelection();
  const cachedPhotos=view==='photos' && switching && !state.photoModeDirty && state.photoItems.length>0;
  if (view==='photos' && !cachedPhotos) {
    state.photoItems=[]; state.photoCursor=state.photoPreviousCursor=''; state.photoLoading=false; state.photoWindowStart=state.photoWindowEnd=0;
    state.photoAnchor=restored?.anchor?.id || '';state.photoFallbackDate=restored?.anchor?.date||'';
    if(restored?.anchor){state.photoJumpAnchor=restored.anchor.id;state.photoJumpOffset=restored.anchor.top;}
  }
  renderMain(); renderDeck();
  if (view==='trash') loadTrash();
  if (view==='takeout') refreshTakeout();
  const restoreScroll=()=>{
    if(state.view!==view) return;
    requestAnimationFrame(()=>{
      const anchor=restored?.anchor && [...document.querySelectorAll('.photo-grid [data-select-file]')].find(el=>el.dataset.selectFile===restored.anchor.id);
      if(anchor)window.scrollBy({top:anchor.getBoundingClientRect().top-restored.anchor.top,behavior:'auto'});
      else if(!restored?.anchor)window.scrollTo({top:restored?.scroll || 0,behavior:'auto'});
      if(view==='photos')schedulePhotoGridWindow();
    });
  };
  if(view==='library' && live && state.unlocked) loadLibrary().then(()=>{if(state.view==='library'){renderMain();restoreScroll();}}).catch(err=>toast(err.message));
  if(view==='photos') {
    loadPhotoAlbums(false); loadPhotoPreparation();
    if(cachedPhotos)restoreScroll(); else loadPhotoPage(true).then(()=>{state.photoModeDirty=false;restoreScroll();});
  }
  history[replace?'replaceState':'pushState']({},'',routeHash(view));
}

let activeMedia = null;

function stopMediaPlayback() {
  document.querySelectorAll('.grid-media-player, .file-preview-media').forEach(media => {
    media.pause();
    media.currentTime = 0;
  });
  activeMedia = null;
}

document.addEventListener('play', event => {
  const media = event.target;
  if (!(media instanceof HTMLMediaElement)) return;
  if (activeMedia && activeMedia !== media) activeMedia.pause();
  activeMedia = media;
}, true);

document.addEventListener('error', event => {
  const media = event.target;
  if (!(media instanceof HTMLMediaElement) || !media.classList.contains('grid-media-player')) return;
  const path = media.dataset.mediaPath || '';
  const fallback = document.createElement('div');
  fallback.className = 'grid-media-fallback';
  fallback.innerHTML = `<strong>Playback unavailable</strong><span>Try the original file.</span><a href="/api/library?path=${encodeURIComponent(path)}" download>Download</a>`;
  media.replaceWith(fallback);
}, true);

function selectFile(id) {
  state.photoSelection = null;
  state.selected = {type: 'file', id};
  state.selectedFiles = [id];
  state.selectionAnchor = id;
  state.minted = null;
  renderMain();
  renderDeck();
}

function selectFolder(path) {
  state.selected = {type: 'folder', path};
  state.selectedFiles = [];
  state.selectionAnchor = '';
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
    : [{title: name, size: fileByID(state.selected.id)?.size || '', kind: fileByID(state.selected.id)?.kind || 'FILE'}];
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
  modeMemory.clear();
  stopWork();
  libraryLoadController?.abort();
  stopMediaPlayback();
  stopPreviewWarming();
  libraryEventSource?.close();
  libraryEventSource = null;
  if (live) {
    try { await engine.lock(); } catch (err) { toast(err.message); return; }
  }
  clearFileSelection();
  photoSelectionRequest++;
  photoPageController?.abort();
  state.photoCamera=state.photoSearchType=state.photoFrom=state.photoTo='';
  state.photoOutsideAlbums=false;
  state.unlocked = false;
  if (state.photosMode === 'hidden') {
    state.photosMode = 'all';
    state.photosAlbum = '';
    state.photoDate = '';
    if (location.hash.startsWith('#photos/hidden') || history.state?.returnHash === '#photos/hidden') history.replaceState({}, '', '#photos');
  }
  $('.app').hidden = true;
  $('#unlock-screen').hidden = false;
  $('#unlock-form').reset();
  if (live && state.authenticated) setVaultOnly(true);
  renderDeck();
  toast(live ? 'Vault locked. Key zeroed.' : 'Vault locked. Key zeroed in this preview.');
}

let libraryPageRequest = 0;
let librarySearchTimer = 0;
let libraryLoadController = null;
async function loadLibrary(reset = true) {
  if (!live) return;
  const username = state.username, currentPath = state.currentPath, request = ++libraryPageRequest;
  const hadSelection = !!state.selectedFiles?.length || state.selected?.type === 'folder';
  libraryLoadController?.abort();
  const controller = new AbortController();
  libraryLoadController = controller;
  const filtered = !!state.librarySearch.trim() || state.libraryType !== 'all' || state.libraryDate !== 'all' || state.librarySize !== 'all' || state.libraryScope !== 'all';
    if (reset) {
    state.libraryCursor = '';
    state.libraryNextCursor = '';
    files.splice(0, files.length);
  }
  state.libraryLoading = true;
  if (state.view === 'library') renderMain();
  try {
    const page = filtered
      ? await engine.searchLibraryPage({query: state.librarySearch, scope: state.libraryScope === 'folder' ? currentPath : '', type: state.libraryType, date: state.libraryDate, size: state.librarySize, cursor: reset ? '' : state.libraryCursor, sort: state.librarySort, descending: state.librarySortDir === 'desc', signal: controller.signal})
      : await engine.listLibraryPage(currentPath, {cursor: reset ? '' : state.libraryCursor, sort: state.librarySort, descending: state.librarySortDir === 'desc', signal: controller.signal});
    if (request !== libraryPageRequest || !state.unlocked || state.username !== username || state.view !== 'library' || state.currentPath !== currentPath) return;
    if (reset) files.splice(0, files.length, ...page.files);
    else files.push(...page.files);
    if (reset && hadSelection) {
      const paths = new Set(files.map(file => file.path));
      const missingFile = (state.selectedFiles || []).some(id => !files.some(file => file.id === id));
      const missingFolder = state.selected?.type === 'folder' && !paths.has(state.selected.path);
      if (missingFile || missingFolder) {
        clearFileSelection();
        toast('Selection cleared because the Library view changed.');
      }
    }
    state.libraryNextCursor = page.next_cursor || '';
    state.libraryCursor = state.libraryNextCursor;
    state.libraryGeneration = page.generation || 0;
  } catch (err) {
    if (err.name === 'AbortError') return;
    if (!reset && /(folder|search) changed/i.test(err.message || '')) {
      state.libraryLoading = false;
      return loadLibrary(true);
    }
    throw err;
  } finally {
    if (request === libraryPageRequest && state.username === username) {
      state.libraryLoading = false;
      libraryLoadController = null;
      if (state.view === 'library') renderMain();
    }
  }
}

let photoSelectionRequest = 0;
async function selectPhotoDay(day) {
  if (!live || !state.unlocked || state.view !== 'photos') return;
  const request = ++photoSelectionRequest, username = state.username;
  const mode = state.photosMode, album = state.photosAlbum || '', query = state.photoQuery || '';
  const filterKey = JSON.stringify([state.photoCamera,state.photoSearchType,state.photoFrom,state.photoTo,state.photoOutsideAlbums]);
  toast('Selecting the full day…');
  const filter = {camera:state.photoCamera,type:state.photoSearchType,outside_albums:state.photoOutsideAlbums,q:query,album:state.photoOutsideAlbums?'':album,from:day === 'unknown' ? '' : day,to:day === 'unknown' ? '' : day,unknown:day === 'unknown',favorite:mode === 'favorites',hidden:mode === 'hidden',archived:mode === 'archived'};
  const selection = await engine.createPhotoSelection({filter,search:hasPhotoSearchFilters(),mode:['recent','favorites','hidden','archived'].includes(mode)?mode:'all',date:day});
  if (request !== photoSelectionRequest || username !== state.username || !state.unlocked || state.view !== 'photos' || mode !== state.photosMode || album !== (state.photosAlbum || '') || query !== (state.photoQuery || '') || filterKey !== JSON.stringify([state.photoCamera,state.photoSearchType,state.photoFrom,state.photoTo,state.photoOutsideAlbums])) return;
  state.photoSelection = selection;
  state.photoSelectedItems = new Map();
  state.selectedFiles = state.photoItems.filter(item => day === 'unknown' ? !item.captureTime : String(item.captureTime || '').startsWith(day)).map(item=>item.id);
  state.selected = state.selectedFiles.length ? {type:'file',id:state.selectedFiles[0]} : null;
  renderMain(); renderDeck(); toast(`${selection.count} photos selected across the full day.`);
}

let photoAlbumsRequest = 0;
let photoPageRequest = 0;
let photoPageController;
const hasPhotoSearchFilters = () => Boolean(state.photoQuery || state.photoCamera || state.photoSearchType || state.photoFrom || state.photoTo || state.photoOutsideAlbums);
function photoScopeOptions(){return {mode:['favorites','hidden','archived'].includes(state.photosMode)?state.photosMode:'all',date:state.photoDate,album:state.photoOutsideAlbums?'':state.photosAlbum,search:hasPhotoSearchFilters(),q:state.photoQuery,camera:state.photoCamera,type:state.photoSearchType,from:state.photoFrom,to:state.photoTo,outside_albums:state.photoOutsideAlbums};}
const photoTimeline=installPhotoTimeline({state,summaryForMonth:month=>engine.photoTimelineDates({...photoScopeOptions(),month}),jump:async target=>{
 if(!live || !state.unlocked || state.view!=='photos')return;
 const visible=[...document.querySelectorAll('.photo-grid [data-select-file]')].find(el=>el.getBoundingClientRect().bottom>140);
 const item=state.photoItems.find(item=>item.id===visible?.dataset.selectFile);
 history.replaceState({...history.state,photoNavigation:{owner:state.username,anchor:visible?.dataset.selectFile,date:item?.captureTime?.slice(0,10)||'unknown',offset:visible?.getBoundingClientRect().top,scope:photoScopeOptions()}},'',location.hash);
 clearFileSelection();state.photoSeek=target;state.photoAnchor='';
 state.photoJumpOffset=null;
 const request=photoPageRequest+1;
 await loadPhotoPage(true);
 if(request===photoPageRequest && state.view==='photos' && !state.photoError){const item=state.photoItems.find(item=>item.id===state.photoLastJumpAnchor);history.pushState({photoNavigation:{owner:state.username,anchor:state.photoLastJumpAnchor,date:item?.captureTime?.slice(0,10)||'unknown',scope:photoScopeOptions()}},'',routeHash('photos'));restorePhotoJump();}
}});
function restorePhotoJump(){
 const id=state.photoJumpAnchor;
 if(!id)return;
 const index=state.photoItems.findIndex(item=>item.id===id);
 if(index<0)return;
 // The destination may lie outside mounted rows. Use the same layout first.
 const grid=document.querySelector('.photo-grid');
 if(grid){const layout=layoutPhotos(state.photoItems,grid.clientWidth,state.photoDensity);const row=layout.rows.find(row=>row.tiles.some(tile=>tile.index===index));if(row)window.scrollTo({top:window.scrollY+grid.getBoundingClientRect().top+row.top-145,behavior:'auto'});}
 // Mount the destination, then correct against its actual rendered position.
 // Toolbar changes and browser scroll anchoring can shift the estimated layout.
 const request=photoPageRequest;
 requestAnimationFrame(()=>{
   if(request!==photoPageRequest || state.view!=='photos' || state.photoJumpAnchor!==id)return;
   refreshPhotoGrid();
   const tile=[...document.querySelectorAll('.photo-grid [data-select-file]')].find(el=>el.dataset.selectFile===id);
   if(tile)window.scrollBy({top:tile.getBoundingClientRect().top-(state.photoJumpOffset??145),behavior:'auto'});
   state.photoJumpOffset=null;
   state.photoJumpAnchor='';schedulePhotoGridWindow();
 });
}

let photoSearchTimer = 0;
async function loadPhotoPage(reset = false, render = true, direction = 'next') {
  if (!live || !state.unlocked || state.view !== 'photos' || state.photosMode === 'albums' && !state.photosAlbum) return;
  if (state.photoLoading && !reset) return;
  const request = ++photoPageRequest, username = state.username;
  photoPageController?.abort();
  photoPageController = new AbortController();
  const signal = photoPageController.signal;
  const previousViewportAnchor = !reset
    ? [...document.querySelectorAll('.photo-grid [data-select-file]')].map(el => ({id: el.dataset.selectFile, top: el.getBoundingClientRect().top})).find(row => row.top >= 0 && row.top < window.innerHeight)
    : null;
  if (reset) {
    const summaryScope=JSON.stringify(photoScopeOptions());
    if(state.photoSummaryScope!==summaryScope)state.photoDateSummary=null;
    state.photoItems = [];
    state.photoCursor = '';
    state.photoPreviousCursor = '';
    state.photoHasMore = false;
    state.photoError = '';
  }
  state.photoLoading = true;
  if (render) renderMain();
  try {
    const photoMode = ['recent', 'favorites', 'hidden', 'archived'].includes(state.photosMode) ? state.photosMode : 'all';
    const around = reset ? state.photoAnchor : '';
    const requestCursor = direction === 'before' ? state.photoPreviousCursor : state.photoCursor;
    const dateFrom = state.photoDate ? (state.photoDate.length === 4 ? `${state.photoDate}-01-01` : state.photoDate.length === 7 ? `${state.photoDate}-01` : state.photoDate) : '';
    const dateTo = state.photoDate ? (state.photoDate.length === 4 ? `${state.photoDate}-12-31` : state.photoDate.length === 7 ? `${state.photoDate}-${new Date(Number(state.photoDate.slice(0,4)), Number(state.photoDate.slice(5,7)), 0).getDate()}` : state.photoDate) : '';
    const seek=reset?state.photoSeek:null;
    const page = state.photosMode==='recent'
      ? await engine.listPhotos(requestCursor,state.photosAlbum || '',{mode:'recent',around,signal})
      : await engine.navigatePhotos({...photoScopeOptions(),cursor:reset?'':requestCursor,around,...seek,signal});
    if (request !== photoPageRequest || username !== state.username || !state.unlocked || state.view !== 'photos') return;
    const incoming = (page.items || []).map(engine.toFixture);
    if(reset){state.photoSeek=null;state.photoStart=page.start||0;state.photoPosition=page.position||0;if(seek){state.photoJumpAnchor=page.anchor_id||'';state.photoLastJumpAnchor=page.anchor_id||'';}}
    if(direction==='before')state.photoStart=page.start||0;
    if (direction === 'before') {
      state.photoItems.unshift(...incoming);
      if (photoViewerIndex >= 0) photoViewerIndex += incoming.length;
    } else state.photoItems.push(...incoming);
    if (reset) state.photoAnchor = '';
    if (state.photoItems.length > 2000) {
      const trim = state.photoItems.length - 2000;
      if (direction === 'before') state.photoItems.splice(2000, trim);
      else {
        state.photoItems.splice(0, trim);
        state.photoStart=(state.photoStart||0)+trim;
        photoViewerIndex = Math.max(-1, photoViewerIndex - trim);
      }
    }
    if (direction !== 'before') state.photoCursor = page.next_cursor || '';
    state.photoPreviousCursor = page.previous_cursor || (direction === 'before' ? '' : state.photoPreviousCursor);
    state.photoHasMore = Boolean(state.photoCursor);
    state.photoGeneration = page.generation || 0;
    state.photoError = '';
  } catch (err) {
    if (request !== photoPageRequest || username !== state.username || err.name === 'AbortError') return;
    if (err.message?.includes('photo library changed')) {
      state.photoItems = [];
      state.photoCursor = '';
      state.photoPreviousCursor = '';
      state.photoAnchor = '';
      if(state.photoFallbackDate)state.photoSeek={at:state.photoFallbackDate};
      state.photoLoading = false;
      state.photoError = '';
      return loadPhotoPage(true, render);
    }
    state.photoError = err.message;
  } finally {
    if (request === photoPageRequest) state.photoLoading = false;
  }
    if (request === photoPageRequest && render && state.view === 'photos') {
    state.photoViewportRestore = previousViewportAnchor;
    renderMain();
    if (previousViewportAnchor) requestAnimationFrame(() => {
      const el = [...document.querySelectorAll('.photo-grid [data-select-file]')].find(node => node.dataset.selectFile === previousViewportAnchor.id);
      if (el) window.scrollBy({top: el.getBoundingClientRect().top - previousViewportAnchor.top, behavior: 'auto'});
      state.photoViewportRestore = null;
    });
  }
  if (request === photoPageRequest && state.view === 'photos') {schedulePhotoGridWindow();if(reset){await loadPhotoDates();if(request===photoPageRequest)restorePhotoJump();}}
}

let photoDatesRequest=0;
async function loadPhotoDates() {
  if (!live || !state.unlocked) return;
  const username = state.username;
  const request=++photoDatesRequest;
  try {
    const scope=photoScopeOptions(),key=JSON.stringify(scope);
    const summary = await engine.photoTimelineDates(scope);
    if(request!==photoDatesRequest || key!==JSON.stringify(photoScopeOptions()))return;
    if (username !== state.username || !state.unlocked) return;
    state.photoDateSummary = summary;
    state.photoSummaryScope=key;
  } catch {
    if (username !== state.username) return;
    state.photoDateSummary = null;
  }
  if (state.view === 'photos') renderMain();
}

async function loadPhotoPreparation() {
  if (!live || !state.unlocked) return;
  const username=state.username;
  try {const metadata=await engine.photoMetadata();if(username!==state.username || !state.unlocked)return;state.photoMetadata=metadata;}catch{if(username!==state.username || !state.unlocked)return;state.photoMetadata=null;}
  try {const preparation=await engine.photoPreparation();if(username!==state.username || !state.unlocked)return;state.photoPreparation=preparation;} catch {state.photoPreparation=null;}
  if (state.view === 'photos') renderMain();
}

async function loadPhotoAlbums(render = true) {
  if (!live || !state.unlocked) return;
  const request = ++photoAlbumsRequest, username = state.username, hidden = state.photosMode === 'hidden';
  try {
    const result = await engine.listPhotoAlbums(hidden);
    if (request !== photoAlbumsRequest || username !== state.username || !state.unlocked || hidden !== (state.photosMode === 'hidden')) return;
    state.photoAlbums = result.albums || [];
    state.photoAlbumsError = '';
  } catch (err) {
    if (request !== photoAlbumsRequest || username !== state.username || !state.unlocked || hidden !== (state.photosMode === 'hidden')) return;
    state.photoAlbums = [];
    state.photoAlbumsError = err.message;
  }
  state.photoAlbumsLoaded = true;
  if (render && state.view === 'photos') renderMain();
}

async function syncLibraryFromChange() {
  if (!live || !state.unlocked) return;
  if (librarySyncRunning) {
    librarySyncAgain = true;
    return;
  }
  librarySyncRunning = true;
  const scroll = window.scrollY;
  try {
    if (state.view === 'photos') {
      const oldCount = state.photoItems.length;
      const anchor = [...document.querySelectorAll('.photo-grid [data-select-file]')]
        .map(el => ({id: el.dataset.selectFile, top: el.getBoundingClientRect().top}))
        .find(item => item.top >= 0 && item.top < window.innerHeight);
      await Promise.all([loadPhotoAlbums(false), loadPhotoPreparation()]);
      await loadPhotoPage(true, false);
      const retain = Math.min(oldCount, 2000);
      while (state.photoItems.length < retain && state.photoHasMore && !state.photoError) {
        await loadPhotoPage(false, false);
      }
      renderMain();
      renderDeck();
      if ($('#modal').classList.contains('photo-viewer')) renderPhotoViewer();
      requestAnimationFrame(() => {
        const restored = anchor && [...document.querySelectorAll('.photo-grid [data-select-file]')]
          .find(el => el.dataset.selectFile === anchor.id);
        if (restored) window.scrollBy({top: restored.getBoundingClientRect().top - anchor.top, behavior: 'auto'});
        else window.scrollTo({top: scroll, behavior: 'auto'});
      });
      return;
    }
    await loadLibrary();
    state.selectedFiles = (state.selectedFiles || []).filter(id => files.some(file => file.id === id) || state.photoSelectedItems?.has(id));
    if (state.selected?.type === 'file' && !files.some(f => f.id === state.selected.id)) state.selected = state.selectedFiles.length ? {type: 'file', id: state.selectedFiles[0]} : null;
    if (state.view === 'library' || state.view === 'photos') {
      renderMain();
      renderDeck();
      requestAnimationFrame(() => window.scrollTo({top: scroll, behavior: 'auto'}));
    }
  } catch {}
  finally {
    librarySyncRunning = false;
    if (librarySyncAgain) {
      librarySyncAgain = false;
      syncLibraryFromChange();
    }
  }
}

async function refreshTakeout() {
  if (!live || !state.authenticated || !state.unlocked) return;
  try {
    const result = await engine.listTakeout();
    state.takeoutArchives = result.archives || [];
    state.takeoutJobs = result.jobs || [];
    state.takeoutError = '';
    const complete = state.takeoutJobs.filter(job => job.status === 'complete').map(job => `${job.name}:${job.updated_at}`).join('|');
    if (complete && complete !== lastTakeoutComplete) {
      lastTakeoutComplete = complete;
      if (state.view === 'photos') {
        await Promise.all([loadPhotoAlbums(false), loadPhotoPreparation()]);
        await loadPhotoPage(true, false);
        renderMain();
      } else if (state.view === 'library') {
        await loadLibrary();
        renderMain();
      }
    }
  } catch (err) { state.takeoutError = err.message; }
  if (state.view === 'takeout') renderMain();
}

setInterval(() => { if (state.view === 'takeout' && live && state.unlocked) refreshTakeout(); }, 4000);
setInterval(async () => {
  if (state.view !== 'photos' || !live || !state.unlocked) return;
  const username=state.username;
  try {
    const metadata=await engine.photoMetadata();
    if(username!==state.username || !state.unlocked)return;
    state.photoMetadata=metadata;
    const metadataStatus=document.querySelector('[data-photo-metadata-status]');
    if(metadataStatus && state.photoMetadata.status!=='idle'){const job=state.photoMetadata;metadataStatus.textContent=`${job.examined} / ${job.total} dates · ${job.status.replaceAll('_',' ')} · ${job.unresolved} unknown · ${job.failed} failed`;}
    const metadataButton=document.querySelector('[data-action="photo-metadata"].secondary');
    if(metadataButton){const active=['queued','running'].includes(metadata.status),paused=['paused','paused_error'].includes(metadata.status);metadataButton.dataset.metadataAction=active?'pause':paused?'resume':'start';metadataButton.textContent=active?'Pause date repair':paused?'Resume date repair':'Repair dates';}
    const preparation=await engine.photoPreparation();
    if(username!==state.username || !state.unlocked)return;
    state.photoPreparation=preparation;
    const prep = state.photoPreparation;
    const status = document.querySelector('[data-photo-prep-status]');
    const button = document.querySelector('[data-action="photo-preparation"]');
    if (status) status.textContent = prep.enabled ? `${prep.ready} / ${prep.total} previews · ${prep.status.replaceAll('_', ' ')}${prep.working ? ` · ${prep.working} rendering · ${prep.work_progress}% avg` : ''}${prep.failed ? ` · ${prep.failed} failed` : ''}${prep.error ? ` · ${prep.error}` : ''}` : 'Prepare previews';
    if (button) {
      const active = prep.enabled && ['queued', 'running', 'paused_storage'].includes(prep.status);
      button.dataset.prepAction = active ? 'pause' : 'resume';
      button.textContent = active ? 'Pause preparation' : 'Prepare previews';
      let retry = document.querySelector('[data-prep-action="retry"]');
      if (prep.failed && !retry) {
        retry = document.createElement('button');
        retry.className = 'secondary';
        retry.dataset.action = 'photo-preparation';
        retry.dataset.prepAction = 'retry';
        retry.textContent = 'Retry failed previews';
        button.after(retry);
      } else if (!prep.failed) retry?.remove();
    }
  } catch {}
}, 5000);

function startLibraryEvents() {
  libraryEventSource?.close();
  if (!live || !state.unlocked) return;
  libraryEventSource = engine.libraryEvents(change => {
    if (state.view !== 'photos') { state.photoModeDirty=true; state.photoItems=[]; }
    if (['photo-visibility','rename','delete','restore'].includes(change?.kind)) {
      modeMemory.clearSelections();
      photoSelectionRequest++;
      clearFileSelection();
      photoGrabIDs=[];
      if ($('#photo-grab-form') || $('#photo-album-members-form') || $('#modal').querySelector('.duplicate-group')) $('#modal').close();
      if ($('#modal').classList.contains('photo-viewer')) closePhotoViewer();
    }
    syncLibraryFromChange();
  });
}

async function loadQuota() {
  if (!live) return;
  const username = state.username;
  try {
    const quota = await engine.quota();
    if (state.unlocked && state.username === username) { state.quota = quota; renderDeck(); }
  } catch (err) { if (state.username === username) toast(err.message); }
}

async function loadTrash() {
  if (!live) return;
  try {
    const result = state.trashScope === 'photos' ? await engine.listPhotoTrash(state.trashHidden) : await engine.listTrash();
    state.trash = result.items || [];
    state.trashRetention = result.retention_days || 30;
    renderMain();
  } catch (err) { toast(err.message); }
}

function openDesk() {
  modeMemory.clear();
  clearFileSelection();
  state.unlocked = true;
  state.photoItems = [];
  state.photoCursor = '';
  state.photoPreparation = null;
  state.photoMetadata=null;state.photoJumpAnchor='';state.photoLastJumpAnchor='';state.photoSeek=null;state.photoFallbackDate='';
  state.photoAlbums = [];
  state.photoAlbumsLoaded = false;
  state.photoAlbumsError = '';
  state.photoDateSummary = null;
  $('#unlock-screen').hidden = true;
  $('.app').hidden = false;
  $('#admin-nav').hidden = !state.admin;
  if (state.username) $('#username').innerHTML = `${esc(state.fullName || state.username)}<small>Open · this session owns the key</small>`;
  renderMain();
  renderDeck();
  startLibraryEvents();
  if (state.view === 'photos') {
    loadPhotoAlbums();
    loadPhotoDates();
    loadPhotoPreparation();
    loadPhotoPage(true);
  } else if (state.view === 'library') {
    loadLibrary().then(() => { if (state.view === 'library') renderMain(); }).catch(err => toast(err.message));
  }
  if (live) void Promise.allSettled([loadQuota(), loadCapsules(), refreshPlaces(), loadAccessRequests(), setUploadStorageUser(state.username)]);
}

async function loadAccessRequests() {
  if (!live || !state.admin) return;
  try { state.requests = await engine.listAccessRequests(); } catch (err) { toast(err.message); }
  try { state.adminUsers = await engine.listAdminUsers(); } catch (err) { toast(err.message); }
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
  if (kind === 'batch-move' || kind === 'batch-download' || kind === 'batch-delete') { await runBatchAction(kind.slice('batch-'.length)); return; }
  if (kind === 'send-file') sendFile(key);
  if (kind === 'send-folder') sendFolder(key);
  if (kind === 'preview-file') previewFile(key);
  if (kind === 'download-folder') {
    if (live) await createArchiveJob([key]);
    else toast('Download is the engine. This preview has no bytes.');
  }
  if (kind === 'copy-file') { setClipboard([filePath(key)], 'copy'); }
  if (kind === 'copy-folder') { setClipboard([key], 'copy'); }
  if (kind === 'details-file') detailsFile(key);
  if (kind === 'details-folder') detailsFolder(key);
  if (kind === 'paste') { await pasteClipboard(); }
  if (kind === 'download') {
    const f = fileByID(key);
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
    if (live) { try { await resolveLibraryOperation(destination => engine.renameLibrary(from, destination), to); state.undo = {kind: 'rename', from: to, to: from}; await loadLibrary(); renderMain(); toast('Renamed.'); } catch (err) { toast(err.message); } }
    else toast('Renamed in the connected node.');
  }
  if (kind === 'delete-file' || kind === 'delete-folder') {
    const f = fileByID(key);
    const path = kind === 'delete-folder' ? key : (f ? filePath(key) : key);
    if (!confirm(`Move ${path} to Trash? It remains recoverable for ${state.trashRetention} days.`)) return;
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
  if (kind === 'photo-folder-hidden') {
    const folder = files.find(file => file.folder && file.path === key);
    try {
      await engine.setPhotoFolderHidden(key, !folder?.hidden);
      await loadLibrary(true);
      renderMain();
      toast(folder?.hidden ? 'Folder is visible in Photos.' : 'Folder hidden from Photos.');
    } catch (err) { toast(err.message); }
  }
  if (kind === 'photos-add-folder') {
    const fallback = key.split('/').pop() || 'Imported folder';
    const name = prompt('Copy this folder into Photos as:', fallback);
    if (!name || !live || !state.unlocked) return;
    const clean = name.trim().replace(/^\/+|\/+$/g, '');
    if (!clean || clean.includes('/')) { toast('Choose a single folder name.'); return; }
    const destination = `Photos/${clean}`;
    if (!confirm(`Copy ${key} into ${destination}? The original stays in the Library.`)) return;
    try {
      await engine.copyLibrary(key, destination);
      if (state.view === 'library') await loadLibrary(true);
      await loadPhotoAlbums(false);
      toast('Folder copied into Photos.');
    } catch (err) { toast(err.message); }
    return;
  }
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
  if (b.dataset.uploadReselect !== undefined) { uploadPrefix = ''; $('#upload').click(); return; }
  if (b.dataset.uploadCancel !== undefined) { cancelUploads(); return; }
  if (b.dataset.libraryFilters !== undefined) { state.libraryFiltersOpen = !state.libraryFiltersOpen; renderMain(); return; }
  if (b.dataset.libraryClearFilter) {
    const key = {scope: 'libraryScope', type: 'libraryType', date: 'libraryDate', size: 'librarySize'}[b.dataset.libraryClearFilter];
    if (key) state[key] = 'all';
    if (live && state.unlocked && state.view === 'library') loadLibrary(true).catch(err => toast(err.message));
    else renderMain();
    return;
  }
  if (b.dataset.archiveDownload) { engine.downloadArchive(b.dataset.archiveDownload); return; }
  if (b.dataset.archiveCancel) { engine.cancelArchive(b.dataset.archiveCancel).then(() => renderUploadTray()).catch(err => toast(err.message)); return; }
  if (b.dataset.trashRestore) {
    (state.trashScope === 'photos' ? engine.restorePhotoTrash(b.dataset.trashId, state.trashHidden) : engine.restoreTrash(b.dataset.trashRestore)).then(async () => { await loadLibrary(); await loadTrash(); toast('Restored.'); }).catch(err => toast(err.message));
    return;
  }
  if (b.dataset.action === 'trash-cleanup') {
    if (!confirm(`Permanently remove Trash items older than ${state.trashRetention} days?`)) return;
    engine.cleanupTrash().then(async result => { await loadTrash(); await loadQuota(); toast(`Expired Trash cleaned. ${formatBytes(result.reclaimed_bytes || 0)} reclaimed.`); }).catch(err => toast(err.message));
    return;
  }
  if (b.dataset.action === 'toggle-favorite') {
    const index = state.favorites.indexOf(state.currentPath);
    if (index >= 0) state.favorites.splice(index, 1);
    else state.favorites.push(state.currentPath);
    localStorage.setItem('wzcl-favorites', JSON.stringify(state.favorites));
    renderMain();
    return;
  }
  if (b.closest('form') && !b.dataset.action) return;
  if (b.dataset.view) navigate(b.dataset.view);
  if (b.dataset.openFolder) { setLibraryPath(b.dataset.openFolder); }
  if (b.dataset.libraryPath !== undefined) { setLibraryPath(b.dataset.libraryPath); }
  if (b.dataset.libraryMore !== undefined) { loadLibrary(false).catch(err => toast(err.message)); }
  if (b.dataset.librarySortDir !== undefined) {
    state.librarySortDir = state.librarySortDir === 'asc' ? 'desc' : 'asc';
    if (live && state.unlocked && state.view === 'library') loadLibrary(true).catch(err => toast(err.message));
    else renderMain();
  }
  if (b.dataset.libraryView !== undefined) { state.libraryView = b.dataset.libraryView; renderMain(); }
  if (b.dataset.batch) { runBatchAction(b.dataset.batch); return; }
  if (b.dataset.selectFile) {
    if (e.shiftKey) chooseRange(b.dataset.selectFile);
    else if (e.ctrlKey || e.metaKey) toggleFileSelection(b.dataset.selectFile);
    else if (state.view === 'photos') openPhotoViewer(b.dataset.selectFile);
    else { selectFile(b.dataset.selectFile); previewFile(b.dataset.selectFile); }
  }
  if (b.dataset.selectFolder) selectFolder(b.dataset.selectFolder);
  if (b.dataset.sendFile) sendFile(b.dataset.sendFile);
  if (b.dataset.sendFolder) sendFolder(b.dataset.sendFolder);
  if (b.dataset.revoke) revoke(b.dataset.revoke);
  if (b.dataset.openGrab) {
    const cap = state.capsules.find(c => c.id === b.dataset.openGrab);
    if (cap?.status === 'live') window.open(grabHref(cap.id), '_blank', 'noopener');
  }
  if (b.dataset.takeout) { state.takeout = b.dataset.takeout; renderMain(); }
  if (b.dataset.importZip) {
    engine.startTakeout(b.dataset.importZip).then(() => { toast('Takeout import started.'); refreshTakeout(); }).catch(err => toast(err.message));
    return;
  }
  if (b.dataset.cancelZip) {
    engine.cancelTakeout(b.dataset.cancelZip).then(() => { toast('Stopping import.'); refreshTakeout(); }).catch(err => toast(err.message));
    return;
  }
  if (b.dataset.photoSelectDay) { selectPhotoDay(b.dataset.photoSelectDay).catch(err => toast(err.message)); return; }
  if (b.dataset.action === 'photo-viewer-close') { closePhotoViewer(); return; }
  if (b.dataset.action === 'photo-show-library') { showPhotoInLibrary().catch(err => toast(err.message)); return; }
  if (b.dataset.photoPrefer) { engine.preferPhoto(b.dataset.photoPrefer,state.photosMode==='hidden').then(()=>reviewPhotoDuplicates()).catch(err=>toast(err.message)); return; }
  if (b.dataset.action === 'photo-duplicates') { reviewPhotoDuplicates().catch(err=>toast(err.message)); return; }
  if (b.dataset.action === 'photo-duplicates-more') { reviewPhotoDuplicates(photoDuplicateCursor).catch(err=>toast(err.message)); return; }
  if (b.dataset.action === 'photos-share-selected') { sharePhotos(); return; }
  if (b.dataset.action === 'share-photo-album') { sharePhotoAlbum(b.dataset.albumPath).catch(err=>toast(err.message)); return; }
  if (b.dataset.action === 'new-photo-album') { editPhotoAlbum(); return; }
  if (b.dataset.action === 'edit-photo-album') { editPhotoAlbum(b.dataset.albumId); return; }
  if (b.dataset.action === 'delete-photo-album') {
    if (!confirm('Delete this album? The photos themselves stay in the library.')) return;
    engine.deletePhotoAlbum(b.dataset.albumId, Number(b.dataset.albumRevision)).then(async () => { await loadPhotoAlbums(false); renderMain(); toast('Album deleted.'); }).catch(err => toast(err.message));
    return;
  }
  if (b.dataset.action === 'photo-add-to-album') { addPhotosToAlbum([state.photoItems[photoViewerIndex]?.entryID || state.photoItems[photoViewerIndex]?.id]); return; }
  if (b.dataset.action === 'photos-add-selected-to-album') { addPhotosToAlbum((state.selectedFiles || []).map(id => state.photoItems.find(item => item.id === id)?.entryID || id),state.photoSelection); return; }
  if (b.dataset.action === 'photos-remove-selected-from-album') {
    const album = photoAlbumByID(state.photosAlbum.replace(/^album:/, ''));
    if (!album || !(state.photoSelection?.count || state.selectedFiles?.length)) return;
    const change = state.photoSelection ? engine.photoSelectionAction({selection_id:state.photoSelection.id,hidden:state.photoSelection.hidden,action:'remove_album',album_id:album.id,revision:album.revision}) : engine.editPhotoAlbumMembers({id:album.id, revision:album.revision, remove_ids:state.selectedFiles});
    change.then(async () => { clearFileSelection(); await loadPhotoAlbums(false); await loadPhotoPage(true); toast('Removed from album. Originals stay in the library.'); }).catch(err => toast(err.message));
    return;
  }
  if (b.dataset.action === 'photo-viewer-prev') { movePhotoViewer(-1); return; }
  if (b.dataset.action === 'photo-viewer-next') { movePhotoViewer(1); return; }
  if (b.dataset.action === 'photo-viewer-info') { state.photoViewerInfo = !state.photoViewerInfo; renderPhotoViewer(); return; }
  if (b.dataset.action === 'photo-edit') { editPhotoDetails(); return; }
  if (b.dataset.action === 'photo-edit-cancel') { if ($('#photo-edit-form')) renderPhotoViewer(); else $('#modal').close(); return; }
  if (b.dataset.action === 'photo-toggle-archive') {
    const item = state.photoItems[photoViewerIndex];
    if (!item || !live) { toast('Archive needs an unlocked connected vault.'); return; }
    engine.updatePhoto(item.entryID || item.id, {archived:!item.archived}, state.photosMode === 'hidden').then(updated => {
      Object.assign(item, engine.toFixture(updated)); renderPhotoViewer(); toast(item.archived ? 'Photo archived.' : 'Photo restored to Timeline.');
    }).catch(err => toast(err.message));
    return;
  }
  if (b.dataset.action === 'photo-toggle-favorite') {
    const item = state.photoItems[photoViewerIndex];
    if (!item || !live) { toast('Favorites need an unlocked connected vault.'); return; }
    const favorite = !item.favorite;
    engine.setPhotoFavorite(item.entryID || item.id, favorite, state.photosMode === 'hidden').then(updated => {
      Object.assign(item, engine.toFixture(updated));
      renderPhotoViewer();
      toast(favorite ? 'Added to Favorites.' : 'Removed from Favorites.');
    }).catch(err => toast(err.message));
    return;
  }
  if (b.dataset.action === 'photo-zoom-in' || b.dataset.action === 'photo-zoom-out') {
    const scale = state.photoZoom || 1;
    state.photoZoom = Math.max(1, Math.min(4, scale + (b.dataset.action === 'photo-zoom-in' ? 0.5 : -0.5)));
    renderPhotoViewer(); return;
  }
  if (b.dataset.action === 'photos-more') {
    if (state.photosMode === 'albums' && !state.photosAlbum) { state.photosShown = (state.photosShown || 60) + 60; renderMain(); }
    else loadPhotoPage(false);
    return;
  }
  if (b.dataset.action === 'photos-earlier') { loadPhotoPage(false, true, 'before'); return; }
  if(b.dataset.action==='photo-metadata'){
    engine.setPhotoMetadata(b.dataset.metadataAction).then(job=>{state.photoMetadata=job;renderMain();toast(job.options?.dry_run?'Date inspection started.':'Date repair updated.');}).catch(err=>toast(err.message));return;
  }
  if (b.dataset.action === 'photo-preparation') {
    const action = b.dataset.prepAction || 'start';
    engine.setPhotoPreparation(action).then(result => { state.photoPreparation = result; renderMain(); toast(action === 'pause' ? 'Preview preparation paused.' : 'Preview preparation started.'); }).catch(err => toast(err.message));
    return;
  }
  if (b.dataset.photosMode || b.dataset.photoAlbum) {
    stopMediaPlayback();
    clearFileSelection();
    state.photosMode = b.dataset.photosMode || 'albums';
    state.photosAlbum = b.dataset.photoAlbum || '';
    state.photoDate = '';
    state.photosShown = 60;
    navigate('photos');
    return;
  }
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
    if (live) refreshTakeout();
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
  if (b.dataset.userToggle) engine.setUserDisabled(b.dataset.userToggle, b.dataset.disabled === 'true').then(() => loadAccessRequests().then(renderMain)).catch(err => toast(err.message));
  if (b.dataset.userDelete) {
    const username = b.dataset.username || '';
    if (prompt(`Permanently delete ${username} and all of their data? Type the username to confirm.`) === username) {
      engine.deleteUser(b.dataset.userDelete, username).then(result => {
        toast(result.status === 'deleted' ? `${username} deleted.` : `${username} deletion is pending; cleanup will resume automatically.`);
        return loadAccessRequests().then(renderMain);
      }).catch(err => toast(err.message));
    }
  }
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

document.addEventListener('toggle', e => { if(e.target.matches?.('.photo-search-filters') && e.target.isConnected) state.photoFiltersOpen=e.target.open; }, true);

document.addEventListener('change', e => {
  if (e.target.dataset.photoDensity !== undefined) {
    state.photoDensity = e.target.value === 'compact' ? 'compact' : 'comfortable';
    renderMain();
  }
  if (e.target.dataset.photoFilter) {
    const key=e.target.dataset.photoFilter;
    state[key]=e.target.type==='checkbox'?e.target.checked:e.target.value;
    clearFileSelection();
    loadPhotoPage(true).catch(err=>toast(err.message));
    return;
  }
  if (e.target.dataset.photoDate !== undefined) {
    state.photoDate = e.target.value;
    state.photoFrom=state.photoTo='';
    state.photosMode = 'all';
    state.photosAlbum = '';
    history.pushState({}, '', routeHash('photos'));
    loadPhotoPage(true);
  }
  if (e.target.name === 'gate') {
    state.gate = e.target.value;
    renderMain();
  }
  if (e.target.dataset.librarySort !== undefined) {
    state.librarySort = e.target.value;
    if (live && state.unlocked && state.view === 'library') loadLibrary(true).catch(err => toast(err.message));
    else renderMain();
  }
  if (e.target.dataset.libraryFilter) {
    const key = e.target.dataset.libraryFilter;
    const stateKey = {scope: 'libraryScope', type: 'libraryType', date: 'libraryDate', size: 'librarySize'}[key];
    if (stateKey) {
      state[stateKey] = e.target.value;
      if (live && state.unlocked && state.view === 'library') loadLibrary(true).catch(err => toast(err.message));
      else renderMain();
    }
  }
});

document.addEventListener('input', e => {
  if (e.target.dataset.photoSearch === undefined) return;
  state.photoQuery = String(e.target.value || '').slice(0, 256);
  clearTimeout(photoSearchTimer);
  const selection = e.target.selectionStart;
  photoSearchTimer = setTimeout(async () => {
    if (state.view !== 'photos') return;
    state.photoAnchor = '';
    await loadPhotoPage(true, false);
    if (state.view !== 'photos') return;
    history.replaceState({}, '', routeHash('photos'));
    renderMain();
    const search = $('#photo-search');
    search?.focus({preventScroll:true});
    if (selection !== null && selection !== undefined) search?.setSelectionRange(selection, selection);
  }, 250);
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
    clearTimeout(librarySearchTimer);
    librarySearchTimer = setTimeout(() => {
      if (live && state.unlocked && state.view === 'library') loadLibrary(true).catch(err => toast(err.message));
    }, 300);
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
  if (on) {
    state.photoItems = [];
    state.photoCursor = '';
    state.photoAlbums = [];
    state.photoAlbumsLoaded = false;
    state.photoPreparation = null;
  state.photoMetadata=null;state.photoJumpAnchor='';state.photoLastJumpAnchor='';state.photoSeek=null;state.photoFallbackDate='';
    state.photoDateSummary = null;
  }
  const row = $('#username-row');
  const username = $('#username-row input');
  const forge = $('#forge-mode');
  if (row) row.hidden = on;
  if (username) username.disabled = on;
  if (forge) forge.hidden = on;
  const label = $('#passphrase-label');
  if (label) label.textContent = on ? 'Vault passphrase' : 'Account / vault passphrase';
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
  const submitButton = e.target.querySelector('.primary');
  if (submitButton?.disabled) return;
  const form = new FormData(e.target);
  const username = String(form.get('username') || '').trim();
  const pass = String(form.get('passphrase') || '');
  const confirm = String(form.get('confirm') || '');
  const vaultOnly = live && state.authenticated && !state.forging;
  $('#unlock-error').textContent = '';
  if (!vaultOnly && !username) { $('#unlock-error').textContent = 'Username must not be empty.'; return; }
  if (!pass) { $('#unlock-error').textContent = 'Passphrase must not be empty.'; return; }
  if (state.forging && !confirm) { $('#unlock-error').textContent = 'Confirm the passphrase.'; return; }
  if (state.forging && pass !== confirm) { $('#unlock-error').textContent = 'Passphrases do not match.'; return; }
  if (live) {
    if (submitButton) { submitButton.disabled = true; submitButton.textContent = 'Working…'; }
    e.target.setAttribute('aria-busy', 'true');
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
        setVaultOnly(true);
        try {
          await engine.unlock(pass);
        } catch {
          setVaultOnly(true);
          e.target.querySelector('[name=passphrase]').value = '';
          $('#unlock-error').textContent = 'Account signed in. Unlock this user vault with its vault passphrase.';
          if (submitButton) { submitButton.disabled = false; submitButton.textContent = 'Unlock vault →'; }
          e.target.removeAttribute('aria-busy');
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
        if (submitButton) { submitButton.disabled = false; submitButton.textContent = 'Log in →'; }
        e.target.removeAttribute('aria-busy');
        return;
      }
      if (/does not exist|missing/i.test(msg)) {
        setForgeMode(true);
        $('#unlock-error').textContent = 'No account yet. Create the first local account.';
        if (submitButton) { submitButton.disabled = false; submitButton.textContent = 'Create account →'; }
        e.target.removeAttribute('aria-busy');
        return;
      }
      $('#unlock-error').textContent = msg;
      if (submitButton) { submitButton.disabled = false; submitButton.textContent = state.forging ? 'Create account →' : (state.authenticated ? 'Unlock vault →' : 'Log in →'); }
      e.target.removeAttribute('aria-busy');
      return;
    }
    if (submitButton) { submitButton.disabled = false; submitButton.textContent = state.forging ? 'Create account →' : (state.authenticated ? 'Unlock vault →' : 'Log in →'); }
    e.target.removeAttribute('aria-busy');
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
  if (e.target.id === 'photo-grab-form') {
    e.preventDefault();
    if (!live || !state.unlocked) return;
    const form=e.target, data=new FormData(form), submit=form.querySelector('button.primary');
    submit.disabled=true;submit.textContent='Preparing gallery…';
    const passphrase=String(data.get('passphrase') || '');
    engine.mintPhotoGrab({...(photoGrabSelection ? {selection_id:photoGrabSelection.id} : {ids:photoGrabIDs}),title:String(data.get('title') || 'Photos'),expiry:String(data.get('expiry') || '24h'),grabs:Number(data.get('grabs') || 1),gate:passphrase?'passphrase':'open',passphrase,hidden:photoGrabSelection?.hidden ?? (state.photosMode==='hidden'),confirm_hidden:data.get('confirm_hidden')==='on'}).then(async row=>{
      await loadCapsules();
      modal(`<h2>Gallery ready</h2><p><a href="${esc(row.url)}" target="_blank" rel="noopener">${esc(row.url)}</a></p><img class="mint-qr" src="/api/qr?url=${encodeURIComponent(row.url)}" alt="QR code for this gallery"><div class="dialog-actions"><button class="secondary" type="button" data-close>Done</button></div>`);
    }).catch(err=>{toast(err.message);submit.disabled=false;submit.textContent='Create grab';});
  }
  if (e.target.matches('form#photo-album-form')) {
    e.preventDefault();
    if (!live) { toast('Album editing needs an unlocked connected vault.'); return; }
    const data = new FormData(e.target), id = String(data.get('id') || '');
    const body = {id, revision:Number(data.get('revision') || 0), title:String(data.get('title') || ''), description:String(data.get('description') || ''), position:Number(data.get('position') || 0), cover_id:String(data.get('cover_id') || '')};
    if (!id && photoAlbumPendingSelection) { body.selection_id = photoAlbumPendingSelection.id; body.hidden = photoAlbumPendingSelection.hidden; }
    else if (!id) body.asset_ids = photoAlbumPendingIDs.map(key => state.photoItems.find(item => item.id === key)?.entryID || key);
    engine.savePhotoAlbum(body).then(async () => { $('#modal').close(); await loadPhotoAlbums(false); state.photosMode = 'albums'; state.photosAlbum = ''; renderMain(); toast(id ? 'Album updated.' : 'Album created.'); }).catch(err => toast(err.message));
  }
  if (e.target.id === 'photo-album-members-form') {
    e.preventDefault();
    if (!live) { toast('Album editing needs an unlocked connected vault.'); return; }
    const albumID = String(new FormData(e.target).get('album_id') || ''), album = photoAlbumByID(albumID);
    if (!album) { toast('That album is no longer available.'); return; }
    const addIDs = photoAlbumPendingIDs;
    (photoAlbumPendingSelection ? engine.photoSelectionAction({selection_id:photoAlbumPendingSelection.id,hidden:photoAlbumPendingSelection.hidden,action:'add_album',album_id:albumID,revision:album.revision}) : engine.editPhotoAlbumMembers({id:albumID, revision:album.revision, add_ids:addIDs})).then(async () => { await loadPhotoAlbums(false); $('#modal').close(); renderMain(); toast(`Added ${addIDs.length} photos to ${album.title}.`); }).catch(err => toast(err.message));
  }
  if (e.target.id === 'photo-edit-form') {
    e.preventDefault();
    const item = state.photoItems[photoViewerIndex];
    if (!item || !live) { toast('Editing needs an unlocked connected vault.'); return; }
    const data = new FormData(e.target);
    const captured = String(data.get('captured_at') || '');
    const offset = String(data.get('capture_offset') || '').trim();
    engine.updatePhoto(item.entryID || item.id, {
      caption:String(data.get('caption') || ''),
      captured_at:captured ? `${captured}:00${offset || 'Z'}` : '',
      offset_known:Boolean(offset), rotation:Number(data.get('rotation') || 0),
    }, state.photosMode === 'hidden').then(updated => {
      Object.assign(item, engine.toFixture(updated)); renderPhotoViewer(); toast('Photo details saved.');
    }).catch(err => toast(err.message));
  }
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
  if (e.altKey) return;
  if ($('#modal').open) {
    if ($('#modal').classList.contains('photo-viewer') && !e.ctrlKey && !e.metaKey) {
      if (e.key === 'ArrowLeft') { e.preventDefault(); movePhotoViewer(-1); }
      if (e.key === 'ArrowRight') { e.preventDefault(); movePhotoViewer(1); }
    }
    return;
  }
  if (!state.unlocked) return;
  if (e.target.matches('input,textarea,select') || e.target.isContentEditable) return;
  const modifier = e.ctrlKey || e.metaKey;
  if (modifier && e.key.toLowerCase() === 'z') { e.preventDefault(); undoLast(); return; }
  if (modifier && e.key.toLowerCase() === 'c') {
    const rows = selectedFileRows();
    const paths = rows.map(row => filePath(row.id));
    if (state.selected?.type === 'folder') paths.push(state.selected.path);
    if (paths.length) { e.preventDefault(); setClipboard(paths, 'copy'); }
    return;
  }
  if (modifier && e.key.toLowerCase() === 'x') {
    const rows = selectedFileRows();
    const paths = rows.map(row => filePath(row.id));
    if (state.selected?.type === 'folder') paths.push(state.selected.path);
    if (paths.length) { e.preventDefault(); setClipboard(paths, 'cut'); }
    return;
  }
  if (modifier && e.key.toLowerCase() === 'v') { e.preventDefault(); pasteClipboard(); return; }
  if (modifier) return;
  if (e.key === 'Escape') hideMenu();
  if (e.key === 'Delete' && state.view === 'library') { e.preventDefault(); state.selected?.type === 'folder' ? deleteSelectedFolder() : runBatchAction('delete'); return; }
  if (e.key === 'F2' && state.view === 'library') { e.preventDefault(); renameSelected(); return; }
  if (e.key === '?') help();
  if (e.key.toLowerCase() === 'l') lockVault();
  if (e.key.toLowerCase() === 's') {
    if (state.selected) { state.view === 'send' ? mint() : navigate('send'); }
    else navigate('library');
  }
  if (/^[1-5]$/.test(e.key)) navigate(['home', 'library', 'send', 'capsules', 'places'][Number(e.key) - 1]);
});

$('#modal').addEventListener('close', () => { $('#modal-content').replaceChildren(); $('#modal').classList.remove('wide', 'photo-viewer'); photoViewerIndex = -1; state.photoViewerInfo = false; });
$('#modal').addEventListener('cancel', event => {
  if ($('#modal').classList.contains('photo-viewer') && photoViewerHistoryPushed) {
    event.preventDefault();
    closePhotoViewer();
  }
});
window.addEventListener('popstate', applyRoute);
try { state.favorites = JSON.parse(localStorage.getItem('wzcl-favorites') || '[]').filter(path => typeof path === 'string'); } catch { state.favorites = []; }
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'hidden') {
    if (frame) cancelAnimationFrame(frame);
    frame = 0;
    return;
  }
  if (state.operation === 'mint' && !frame) frame = requestAnimationFrame(tickMint);
  if (state.operation === 'ingest' && !frame) frame = requestAnimationFrame(tickIngest);
});
applyRoute();
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
  if (tree || libraryContent) { e.preventDefault(); showMenu(e.clientX, e.clientY, rootMenu((state.selectedFiles || []).length > 0, !!state.clipboard?.paths?.length)); }
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
  if (s.authenticated) {
    const account = await engine.me().catch(() => null);
    if (account) {
      state.username = account.username || s.username || state.username;
      state.fullName = account.full_name || '';
      state.admin = !!account.admin;
    }
		await setUploadStorageUser(state.username || s.username || '');
    if (!s.unlocked) setVaultOnly(true);
  }
  if (s.authenticated && s.unlocked) {
    if (state.view !== 'photos' && state.view !== 'library') await loadLibrary();
    await loadCapsules();
    await refreshPlaces();
    await loadAccessRequests();
    openDesk();
  } else {
    renderDeck();
  }
});
