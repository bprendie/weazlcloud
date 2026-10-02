const jsonHeaders = { 'X-Weazl-Desk': '1', 'Content-Type': 'application/json' };

export async function probe() {
  try {
    const r = await fetch('/api/status');
    if (!r.ok) return null;
    const s = await r.json();
    if (typeof s.setup !== 'boolean' && typeof s.forged !== 'boolean') return null;
    return s;
  } catch {
    return null;
  }
}

async function post(url, body) {
  const r = await fetch(url, { method: 'POST', headers: jsonHeaders, body: JSON.stringify(body || {}) });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || r.statusText);
  return j;
}

export const forge = (passphrase, confirm) => post('/api/forge', { passphrase, confirm });
export const unlock = passphrase => post('/api/unlock', { passphrase });
export const login = (username, password) => post('/api/login', { username, password });
export const bootstrap = (username, password, vaultPassphrase, confirm) => post('/api/bootstrap', { username, password, vault_passphrase: vaultPassphrase, confirm });
export const requestAccess = (username, note) => post('/api/access-requests', {username, note});
export const listAccessRequests = async () => { const r = await fetch('/api/access-requests'); const j = await r.json().catch(() => ({})); if (!r.ok) throw new Error(j.error || 'requests'); return j.requests || []; };
export const approveAccess = id => post('/api/access-requests/approve', {id});
export const rejectAccess = id => post('/api/access-requests/reject', {id});
export const listAdminUsers = async () => { const r = await fetch('/api/admin/users'); const j = await r.json().catch(() => ({})); if (!r.ok) throw new Error(j.error || 'users'); return j.users || []; };
export const setUserDisabled = (id, disabled) => post('/api/admin/users/disable', {id, disabled});
export const deleteUser = (id, confirm_username) => post('/api/admin/users/delete', {id, confirm_username});
export const completeAccess = body => post('/api/account-setup', body);
export const me = async () => { const r = await fetch('/api/me'); const j = await r.json().catch(() => ({})); if (!r.ok) throw new Error(j.error || 'account'); return j; };
export const saveSettings = body => post('/api/settings', body);
export const rekeyVault = (current, next, confirm) => post('/api/vault/rekey', {current, next, confirm});
export const lock = () => post('/api/lock', {});
export const kit = passphrase => post('/api/kit', { passphrase });

export async function listLibrary() {
  const r = await fetch('/api/library');
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || 'library');
  return j.files || [];
}

export async function listLibraryPage(path = '', options = {}) {
  const query = new URLSearchParams({path, limit: String(options.limit || 100), sort: options.sort || 'name'});
  if (options.descending) query.set('desc', '1');
  if (options.cursor) query.set('cursor', options.cursor);
  const r = await fetch('/api/library/page?' + query.toString(), {signal: options.signal});
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || 'library page');
  return {...j, files: (j.files || []).map(toFixture)};
}

export async function searchLibraryPage(options = {}) {
  const query = new URLSearchParams({
    q: options.query || '', scope: options.scope || '', type: options.type || 'all',
    date: options.date || 'all', size: options.size || 'all',
    sort: options.sort || 'name', limit: String(options.limit || 100),
  });
  if (options.descending) query.set('desc', '1');
  if (options.cursor) query.set('cursor', options.cursor);
  const r = await fetch('/api/library/search/page?' + query.toString(), {signal: options.signal});
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || 'library search');
  return {...j, files: (j.files || []).map(toFixture)};
}

export function libraryEvents(onChange) {
  const source = new EventSource('/api/library/events');
  source.addEventListener('library', event => {
    try { onChange?.(JSON.parse(event.data)); } catch {}
  });
  return source;
}

export async function putLibrary(path, body) {
  const r = await fetch('/api/library?path=' + encodeURIComponent(path), {
    method: 'PUT',
    headers: { 'X-Weazl-Desk': '1' },
    body
  });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || 'put failed');
  return j;
}

export function putLibraryProgress(path, body, onProgress) {
  let xhr;
  const request = new Promise((resolve, reject) => {
    xhr = new XMLHttpRequest();
    xhr.open('PUT', '/api/library?path=' + encodeURIComponent(path));
    xhr.setRequestHeader('X-Weazl-Desk', '1');
    xhr.upload.onprogress = e => { if (e.lengthComputable) onProgress?.(e.loaded, e.total); };
    xhr.onerror = () => reject(new Error('upload failed'));
    xhr.onabort = () => reject(new Error('upload cancelled'));
    xhr.onload = () => {
      onProgress?.(body.size || 0, body.size || 0, 'saving');
      let j = {};
      try { j = JSON.parse(xhr.responseText || '{}'); } catch {}
      if (xhr.status < 200 || xhr.status >= 300) reject(new Error(j.error || 'upload failed'));
      else resolve(j);
    };
    xhr.send(body);
  });
  request.abort = () => xhr?.abort();
  return request;
}

async function uploadJSON(url, options = {}) {
  const r = await fetch(url, options);
  const j = await r.json().catch(() => ({}));
  if (!r.ok) {
    const error = new Error(j.error || r.statusText || 'upload failed');
    if (Number.isFinite(j.offset)) error.offset = j.offset;
    throw error;
  }
  return j;
}

export const createUpload = (path, size, hash = '') => uploadJSON('/api/uploads', {
  method: 'POST', headers: jsonHeaders, body: JSON.stringify({path, size, hash})
});
export const uploadStatus = id => uploadJSON('/api/uploads/' + encodeURIComponent(id));
export const listUploads = () => uploadJSON('/api/uploads');

export async function sha256Hex(blob) {
  const digest = await crypto.subtle.digest('SHA-256', await blob.arrayBuffer());
  return [...new Uint8Array(digest)].map(byte => byte.toString(16).padStart(2, '0')).join('');
}

export function appendUpload(id, offset, body, hash, onProgress) {
  let xhr;
  const request = new Promise((resolve, reject) => {
    xhr = new XMLHttpRequest();
    xhr.open('PATCH', '/api/uploads/' + encodeURIComponent(id));
    xhr.setRequestHeader('X-Weazl-Desk', '1');
    xhr.setRequestHeader('Upload-Offset', String(offset));
    xhr.setRequestHeader('Upload-Chunk-SHA256', hash);
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    xhr.upload.onprogress = e => { if (e.lengthComputable) onProgress?.(offset + e.loaded, offset + e.total, 'sending'); };
    xhr.onerror = () => reject(new Error('upload chunk failed'));
    xhr.onabort = () => reject(new Error('upload cancelled'));
    xhr.onload = () => {
      let j = {};
      try { j = JSON.parse(xhr.responseText || '{}'); } catch {}
      if (xhr.status < 200 || xhr.status >= 300) {
        const error = new Error(j.error || 'upload chunk failed');
        if (Number.isFinite(j.offset)) error.offset = j.offset;
        reject(error);
      } else resolve(j);
    };
    xhr.send(body);
  });
  request.abort = () => xhr?.abort();
  return request;
}

export const finalizeUpload = id => uploadJSON('/api/uploads/' + encodeURIComponent(id) + '/finalize', {method: 'POST', headers: jsonHeaders});
export const cancelUpload = id => uploadJSON('/api/uploads/' + encodeURIComponent(id), {method: 'DELETE', headers: jsonHeaders});

export const listTakeout = () => uploadJSON('/api/takeout');
export const listPhotoAlbums = (hidden=false) => uploadJSON('/api/photos/albums'+(hidden?'?hidden=1':''));
export const createPhotoSelection = body => post('/api/v1/photos/selections',body);
export const photoSelectionAction = body => post('/api/v1/photos/selection-actions',body);
export const savePhotoAlbum = body => post('/api/v1/photos/albums', {action:'save', ...body});
export const editPhotoAlbumMembers = body => post('/api/v1/photos/albums', {action:'members', ...body});
export const deletePhotoAlbum = (id, revision) => post('/api/v1/photos/albums', {action:'delete', id, revision});
export const listPhotos = (cursor = '', album = '', options = {}) => {
  const q = new URLSearchParams({limit: '100'});
  if (cursor) q.set('cursor', cursor);
  if (album) q.set('album', album);
  if (options.mode && options.mode !== 'all') q.set('mode', options.mode);
  if (options.date) q.set('date', options.date);
  if (options.around) q.set('around', options.around);
  return uploadJSON('/api/v1/photos?' + q.toString(), {signal:options.signal});
};
export const searchPhotos = (cursor = '', options = {}) => {
  const q = new URLSearchParams({limit: '100'});
  for (const key of ['q', 'album', 'type', 'from', 'to', 'camera']) if (options[key]) q.set(key, options[key]);
  if (cursor) q.set('cursor', cursor);
  if (options.favorite) q.set('favorite', '1');
  if (options.hidden) q.set('hidden', '1');
  if (options.archived) q.set('archived', '1');
  if (options.unknown) q.set('unknown', '1');
  if (options.outside_albums) q.set('outside_albums','1');
  return uploadJSON('/api/v1/photos/search?' + q.toString(), {signal:options.signal});
};
export const listPhotoDates = hidden => uploadJSON('/api/v1/photos/dates' + (hidden ? '?hidden=1' : ''));
export const photoDetail = (id, hidden) => uploadJSON('/api/v1/photos/assets/' + encodeURIComponent(id) + (hidden ? '?hidden=1' : ''));
export const setPhotoFavorite = (id, favorite, hidden) => post('/api/v1/photos/assets/' + encodeURIComponent(id) + (hidden ? '?hidden=1' : ''), {favorite: Boolean(favorite)});
export const updatePhoto = (id, body, hidden) => post('/api/v1/photos/assets/' + encodeURIComponent(id) + (hidden ? '?hidden=1' : ''), body);
export const setPhotoFolderHidden = (path, hidden) => post('/api/v1/photos/folders', {path, hidden: Boolean(hidden)});
export const photoPreparation = () => uploadJSON('/api/photos/preparation');
export const setPhotoPreparation = action => post('/api/photos/preparation', {action});
export const startTakeout = name => post('/api/takeout', {name});
export const cancelTakeout = name => uploadJSON('/api/takeout', {method: 'DELETE', headers: jsonHeaders, body: JSON.stringify({name})});

export const createFolder = path => post('/api/library/folder', {path});
export const renameLibrary = (from, to) => post('/api/library/rename', {from, to});
export const copyLibrary = (from, to) => post('/api/library/copy', {from, to});
export async function listTrash() {
  const r = await fetch('/api/trash');
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || 'trash');
  return j;
}
export const listPhotoDuplicates = (cursor='', hidden=false) => uploadJSON('/api/v1/photos/duplicates?' + new URLSearchParams({cursor, hidden:hidden?'1':'0'}));
export const preferPhoto = (id,hidden=false) => post('/api/v1/photos/duplicates' + (hidden?'?hidden=1':''), {id});
export const listPhotoTrash = hidden => uploadJSON('/api/v1/photos/trash' + (hidden ? '?hidden=1' : ''));
export const restorePhotoTrash = (id, hidden) => post('/api/v1/photos/trash/restore' + (hidden ? '?hidden=1' : ''), {id});
export const restoreTrash = path => post('/api/trash/restore', {path});
export async function cleanupTrash() {
  const r = await fetch('/api/trash', {method: 'DELETE', headers: jsonHeaders});
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || 'trash cleanup');
  return j;
}

export async function quota() {
  const r = await fetch('/api/quota');
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || 'quota');
  return j;
}

export async function previewLibrary(path) {
  const r = await fetch('/api/library?path=' + encodeURIComponent(path) + '&preview=1');
  if (!r.ok) {
    const j = await r.json().catch(() => ({}));
    throw new Error(j.error || 'preview failed');
  }
  return {blob: await r.blob(), type: r.headers.get('Content-Type') || 'application/octet-stream'};
}

export async function deleteLibrary(path) {
  const r = await fetch('/api/library?path=' + encodeURIComponent(path), {
    method: 'DELETE',
    headers: { 'X-Weazl-Desk': '1' }
  });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || 'delete failed');
}

export async function createArchive(paths) {
  const r = await fetch('/api/library/archive', {
    method: 'POST',
    headers: jsonHeaders,
    body: JSON.stringify({paths})
  });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || 'archive failed');
  return j;
}

export async function archiveStatus(id) {
  const r = await fetch('/api/library/archive?id=' + encodeURIComponent(id));
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || 'archive status failed');
  return j;
}

export async function cancelArchive(id) {
  const r = await fetch('/api/library/archive?id=' + encodeURIComponent(id), {method: 'DELETE', headers: jsonHeaders});
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || 'archive cancel failed');
}

export function downloadArchive(id) {
  const a = document.createElement('a');
  a.href = '/api/library/archive?id=' + encodeURIComponent(id) + '&download=1';
  a.download = `weazlcloud-${id}.zip`;
  a.rel = 'noopener';
  a.click();
}

export async function downloadLibrary(path, name) {
  const a = document.createElement('a');
  a.href = '/api/library?path=' + encodeURIComponent(path);
  if (name) a.download = name;
  a.rel = 'noopener';
  a.click();
}

export async function listCapsules() {
  const r = await fetch('/api/capsules');
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || 'capsules');
  return j;
}

export const mintPhotoGrab = body => post('/api/v1/photos/grabs', body);
export const mintCapsule = body => post('/api/capsules', body);

export async function revokeCapsule(id) {
  const r = await fetch('/api/capsules?id=' + encodeURIComponent(id), {
    method: 'DELETE',
    headers: { 'X-Weazl-Desk': '1' }
  });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || 'revoke failed');
}

export async function loadPlaces() {
  const r = await fetch('/api/places');
  if (!r.ok) return null;
  return r.json();
}

export const savePlaces = body => post('/api/places', body);
export async function loadNodeSettings() { const r = await fetch('/api/node'); const j = await r.json().catch(() => ({})); if (!r.ok) throw new Error(j.error || 'node settings'); return j; }
export const saveNodeSettings = hostname => post('/api/node', {hostname});

export function toFixture(row) {
  const parts = String(row.path || '').split('/').filter(Boolean);
  const title = parts.pop() || row.path;
  if (row.folder) return {id: 'folder:' + row.path, path: row.path, title, folders: parts, kind: 'DIR', size: 'folder', folder: true, hidden: Boolean(row.hidden), mtime: row.mtime};
  const ext = title.includes('.') ? title.slice(title.lastIndexOf('.') + 1).toUpperCase() : 'FILE';
  const size = row.size >= 1048576 ? `${(row.size / 1048576).toFixed(1)} MB` : row.size >= 1024 ? `${Math.round(row.size / 1024)} KB` : `${row.size} B`;
  return { components:row.components||[], parentAssetID:row.parent_asset_id, previewIdentity:row.preview_identity, thumbHash:row.thumbhash, id: row.id || row.path, path: row.path, entryID: row.id || '', title, folders: parts, kind: ext.slice(0, 3), size, bytes: row.size, mtime: row.mtime || row.modified, importedAt: row.imported_at, captureTime: row.captured_at, captureOffsetMinutes:row.capture_offset_minutes, revision:row.revision, captureSource: row.capture_source, mediaType: row.media_type, width: row.width, height: row.height, favorite: row.favorite, archived: row.archived, caption: row.caption, durationMillis: row.duration_millis, userRotation: row.user_rotation || 0, orientation: row.orientation };
}

const photoNavigationQuery = options => {
 const query=new URLSearchParams();
 for(const key of ['mode','date','album','q','camera','type','from','to','month','around','at','cursor','rank','limit'])if(options[key]!==undefined && options[key]!==null && options[key]!=='')query.set(key,String(options[key]));
 for(const key of ['search','outside_albums','unknown'])if(options[key])query.set(key,'1');
 return query.toString();
};
export const navigatePhotos = options => uploadJSON('/api/v1/photos/seek?'+photoNavigationQuery(options),{signal:options.signal});
export const photoTimelineDates = options => uploadJSON('/api/v1/photos/dates?'+photoNavigationQuery(options),{signal:options.signal});
export const photoMetadata = () => uploadJSON('/api/v1/photos/metadata-jobs');
export const setPhotoMetadata = action => post('/api/v1/photos/metadata-jobs',{action});

export const livePhotoJob = action => action ? post("/api/v1/photos/live-jobs",{action}) : uploadJSON("/api/v1/photos/live-jobs");
export const linkLivePhoto = body => post("/api/v1/photos/live-pair",body);
