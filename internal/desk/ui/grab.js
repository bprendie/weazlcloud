import {grabs, escapeHTML as esc} from './data.js';

const $ = s => document.querySelector(s);
const params = new URLSearchParams(location.search);
const token = params.get('c') || 'dinner';
let stored = {};
try { stored = JSON.parse(localStorage.getItem('wzcl-capsules') || '{}'); } catch { stored = {}; }
const cap = stored[token] || grabs[token] || {
  token,
  name: token,
  kind: 'file',
  size: 'unknown',
  gate: 'open',
  status: 'live',
  expiry: '24 hours left',
  files: [{title: token, size: '', kind: 'FILE'}]
};

const title = $('#grab-title');
const meta = $('#grab-meta');
const form = $('#grab-form');
const list = $('#grab-list');
const actions = $('#grab-actions');
const error = $('#grab-error');

function burned() {
  title.textContent = 'This grab is gone.';
  meta.textContent = cap.expiry || 'Burned after read.';
  form.hidden = true;
  list.hidden = true;
  actions.innerHTML = '';
}

function live() {
  title.textContent = cap.name;
  meta.textContent = `${cap.size} · ${cap.expiry} · ${cap.gate === 'passphrase' ? 'passphrase' : 'open'} · read-only`;
  form.hidden = cap.gate !== 'passphrase';
  if (cap.kind === 'folder') {
    list.hidden = false;
    list.innerHTML = cap.files.map(f => `<div class="grab-file"><span class="kind">${esc(f.kind)}</span><span><strong>${esc(f.title)}</strong><small>${esc(f.size)}</small></span></div>`).join('');
  }
  const label = cap.kind === 'folder' ? 'Grab folder →' : 'Grab →';
  actions.innerHTML = `<button class="primary" id="do-grab" type="button">${label}</button>`;
  $('#do-grab').onclick = grab;
}

function grab() {
  error.textContent = '';
  if (cap.gate === 'passphrase') {
    const phrase = String(new FormData(form).get('phrase') || '');
    if (phrase !== (cap.phrase || '')) {
      error.textContent = 'Wrong passphrase. Phrase rides a second channel.';
      return;
    }
  }
  title.textContent = 'Grabbed.';
  meta.textContent = 'Preview only. No bytes left this machine. A real link would burn after this.';
  form.hidden = true;
  list.hidden = true;
  actions.innerHTML = '';
}

if (cap.status !== 'live') burned();
else live();
