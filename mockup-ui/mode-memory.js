const locationKey=(mode,state)=>JSON.stringify(mode==='library'?[state.currentPath || '']:[state.photosMode || 'all',state.photosAlbum || '',state.photoDate || '',state.photoQuery || '',state.photoCamera || '',state.photoSearchType || '',state.photoFrom || '',state.photoTo || '',Boolean(state.photoOutsideAlbums)]);
// Session-only mode memory. Nothing private goes into localStorage/history.
export class ModeMemory {
  constructor() { this.saved = new Map(); }
  remember(mode, state, scroll, anchor = null) {
    if (!['library','photos'].includes(mode) || !state.unlocked) return;
    this.saved.set(mode, {owner:state.username,location:locationKey(mode,state),scroll,anchor,
      selected:state.selected ? {...state.selected} : null,
      selectedFiles:[...(state.selectedFiles || [])],selectionAnchor:state.selectionAnchor,
      photoSelection:mode === 'photos' && state.photoSelection ? {...state.photoSelection} : null,
      photoSelectedItems:mode === 'photos' ? new Map(state.photoSelectedItems || []) : new Map()});
  }
  restore(mode, state) {
    const saved = this.saved.get(mode);
    if (!saved || saved.owner !== state.username || saved.location !== locationKey(mode,state) || !state.unlocked) return null;
    state.selected = saved.selected ? {...saved.selected} : null;
    state.selectedFiles = [...saved.selectedFiles];
    state.selectionAnchor = saved.selectionAnchor;
    state.photoSelection = saved.photoSelection ? {...saved.photoSelection} : null;
    state.photoSelectedItems = new Map(saved.photoSelectedItems);
    return {scroll:saved.scroll,anchor:saved.anchor};
  }
  clearSelections() {
    for (const value of this.saved.values()) {
      value.selected = value.photoSelection = null;
      value.selectedFiles = []; value.selectionAnchor = ''; value.photoSelectedItems.clear();
    }
  }
  clear() { this.saved.clear(); }
}
