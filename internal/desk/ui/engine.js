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

export const createFolder = path => post('/api/library/folder', {path});
export const renameLibrary = (from, to) => post('/api/library/rename', {from, to});

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

export async function downloadLibrary(path, name) {
  const r = await fetch('/api/library?path=' + encodeURIComponent(path));
  if (!r.ok) {
    const j = await r.json().catch(() => ({}));
    throw new Error(j.error || 'download failed');
  }
  const blob = await r.blob();
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = name || path.split('/').pop();
  a.click();
}

export async function listCapsules() {
  const r = await fetch('/api/capsules');
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || 'capsules');
  return j;
}

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
  if (row.folder) return {id: 'folder:' + row.path, path: row.path, title, folders: parts, kind: 'DIR', size: 'folder', folder: true, mtime: row.mtime};
  const ext = title.includes('.') ? title.slice(title.lastIndexOf('.') + 1).toUpperCase() : 'FILE';
  const size = row.size >= 1048576 ? `${(row.size / 1048576).toFixed(1)} MB` : row.size >= 1024 ? `${Math.round(row.size / 1024)} KB` : `${row.size} B`;
  return { id: row.path, title, folders: parts, kind: ext.slice(0, 3), size, bytes: row.size, mtime: row.mtime };
}
