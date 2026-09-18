export const files = [];

export const grabs = {
  setlist: {
    token: 'setlist',
    name: 'gil-setlist.md',
    kind: 'file',
    size: '8 KB',
    gate: 'passphrase',
    phrase: 'nug',
    status: 'live',
    expiry: '18 hours left',
    files: [{title: 'gil-setlist.md', size: '8 KB', kind: 'MD'}]
  },
  dinner: {
    token: 'dinner',
    name: 'family-dinner.jpg',
    kind: 'file',
    size: '4.1 MB',
    gate: 'open',
    status: 'live',
    expiry: '24 hours left',
    files: [{title: 'family-dinner.jpg', size: '4.1 MB', kind: 'IMG'}]
  },
  vacation: {
    token: 'vacation',
    name: 'Pictures/2026',
    kind: 'folder',
    size: '10.9 MB · 2 files',
    gate: 'open',
    status: 'live',
    expiry: '3 days left',
    files: [
      {title: 'family-dinner.jpg', size: '4.1 MB', kind: 'IMG'},
      {title: 'beach.jpg', size: '6.8 MB', kind: 'IMG'}
    ]
  },
  gone: {
    token: 'gone',
    name: 'old-nug.pdf',
    kind: 'file',
    size: '120 KB',
    gate: 'open',
    status: 'burned',
    expiry: 'burned after 1 grab',
    files: []
  }
};

export const takeouts = [
  {
    id: 'google',
    name: 'Google Takeout',
    detail: 'Drive + Photos dump. Albums each hold a copy.',
    files: '4,812',
    apparent: '38 GB',
    unique: '12 GB',
    note: 'Same beach.jpg in three albums. Restic keeps one.'
  },
  {
    id: 'icloud',
    name: 'iCloud export',
    detail: 'Apple “download a copy.” Originals + JPEGs.',
    files: '2,104',
    apparent: '21 GB',
    unique: '14 GB',
    note: 'HEIC plus the JPEG they mailed you. One original wins.'
  },
  {
    id: 'onedrive',
    name: 'OneDrive zip',
    detail: 'Privacy-dashboard export, or a folder download.',
    files: '931',
    apparent: '9 GB',
    unique: '8.4 GB',
    note: 'Documents you already put in the library store once.'
  }
];

const previewFiles = [
  {id: 'f1', title: 'Invoice-2026-03.pdf', folders: ['Documents', 'Finance'], kind: 'PDF', size: '240 KB'},
  {id: 'f2', title: 'notes.md', folders: ['Documents', 'Projects'], kind: 'MD', size: '12 KB'},
  {id: 'f3', title: 'contract-draft.docx', folders: ['Documents', 'Work'], kind: 'DOC', size: '88 KB'},
  {id: 'f4', title: 'family-dinner.jpg', folders: ['Pictures', '2026'], kind: 'IMG', size: '4.1 MB'},
  {id: 'f5', title: 'beach.jpg', folders: ['Pictures', '2026'], kind: 'IMG', size: '6.8 MB'},
  {id: 'f6', title: 'gil-setlist.md', folders: ['weazldocs'], kind: 'MD', size: '8 KB'},
  {id: 'f7', title: 'setlist-notes.txt', folders: ['Music'], kind: 'TXT', size: '2 KB'}
];

const previewCapsules = [
  {id: 'setlist', label: 'Gil', name: 'gil-setlist.md', kind: 'file', gate: 'passphrase', expiry: '18h left', left: 1, status: 'live'},
  {id: 'dinner', label: 'Open', name: 'family-dinner.jpg', kind: 'file', gate: 'open', expiry: '24h left', left: 1, status: 'live'},
  {id: 'vacation', label: 'Vacation', name: 'Pictures/2026', kind: 'folder', gate: 'open', expiry: '3 days left', left: 5, status: 'live'}
];

export const filePath = f => f.folders.join(' › ');
export const folderLabel = path => path.split('/').join(' › ');
export const folderName = path => path.split('/').pop();
export const filesInFolder = path => files.filter(f => {
  const p = f.folders.join('/');
  return p === path || p.startsWith(path + '/');
});

export const state = {
  engine: false,
  authenticated: false,
  view: 'home',
  unlocked: false,
  forging: false,
  operation: 'idle',
  percent: 0,
  started: 0,
  lanes: {a: 0, b: 0, c: 0},
  dedupe: 0,
  expanded: [],
  selected: null,
  gate: 'open',
  passphrase: '',
  expiry: '24h',
  grabs: '1',
  label: 'Gil',
  minted: null,
  tokenShown: false,
  grabBase: '',
  driveBase: '',
  driveToken: '',
  takeout: 'google',
  destroyId: '',
  capsules: []
};

export function seedPreview() {
  files.splice(0, files.length, ...previewFiles.map(f => ({...f, folders: [...f.folders]})));
  state.engine = false;
  state.capsules = previewCapsules.map(c => ({...c}));
  state.expanded = ['Documents', 'Pictures', 'weazldocs'];
  state.grabBase = 'https://grab.weazl.example';
  state.driveBase = 'davs://drive.weazl.example';
  state.driveToken = 'wzcv-7n3k-mock-token';
  state.dedupe = 41;
  state.lanes.b = 41;
  state.destroyId = 'setlist';
}

export const escapeHTML = value => String(value).replace(/[&<>"']/g, c => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[c]));

export function selectedName() {
  if (!state.selected) return '';
  if (state.selected.type === 'folder') return state.selected.path;
  return files.find(f => f.id === state.selected.id)?.title || '';
}

export function liveCapsules() {
  return state.capsules.filter(c => c.status === 'live');
}
