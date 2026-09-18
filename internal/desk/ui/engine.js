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

export function toFixture(row) {
  const parts = String(row.path || '').split('/').filter(Boolean);
  const title = parts.pop() || row.path;
  const ext = title.includes('.') ? title.slice(title.lastIndexOf('.') + 1).toUpperCase() : 'FILE';
  const size = row.size >= 1048576 ? `${(row.size / 1048576).toFixed(1)} MB` : row.size >= 1024 ? `${Math.round(row.size / 1024)} KB` : `${row.size} B`;
  return { id: row.path, title, folders: parts.length ? parts : ['library'], kind: ext.slice(0, 3), size };
}
