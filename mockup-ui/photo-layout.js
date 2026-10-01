const layouts = new WeakMap();
export const photoDay = item => item.captureTime ? String(item.captureTime).slice(0, 10) : 'unknown';

export function photoAspect(item) {
  let ratio = item.width > 0 && item.height > 0 ? item.width / item.height : 1;
  if (item.orientation >= 5 && item.orientation <= 8) ratio = 1 / ratio;
  if ((item.userRotation || 0) % 180) ratio = 1 / ratio;
  return Math.max(0.25, Math.min(5, ratio));
}

export function layoutPhotos(items, width, density = 'comfortable', order = 'capture') {
  width = Math.max(160, Math.floor(width || 800));
  const cacheKey = `${width}:${density}:${order}:${items.length}:${items[0]?.id}:${items.at(-1)?.id}`;
  const cached = layouts.get(items);
  if (cached?.key === cacheKey) return cached.layout;
  const target = density === 'compact' ? 140 : 210, gap = 8;
  const rows = [];
  let top = 0, day = '', pending = [], ratioSum = 0;
  const flush = full => {
    if (!pending.length) return;
    const available = Math.max(1, width - gap * (pending.length - 1));
    const height = full ? available / ratioSum : Math.min(target, available / ratioSum);
    const tiles = pending.map(index => ({index, width: photoAspect(items[index]) * height}));
    rows.push({key:`row:${items[pending[0]].id}`, day, top, height, tiles});
    top += height + gap;
    pending = []; ratioSum = 0;
  };
  items.forEach((item, index) => {
    const nextDay = order === 'recent' ? (item.importedAt ? String(item.importedAt).slice(0,10) : 'unknown') : photoDay(item);
    if (nextDay !== day) {
      flush(false); day = nextDay;
      rows.push({key:`day:${day}:${item.id}`, day, top, height:36, tiles:[]});
      top += 44;
    }
    pending.push(index); ratioSum += photoAspect(item);
    if (ratioSum * target + gap * (pending.length - 1) >= width || pending.length >= 24) flush(true);
  });
  flush(false);
  const layout = {rows, height:Math.max(0, top - gap), width};
  layouts.set(items, {key:cacheKey, layout});
  return layout;
}

export function photoRowWindow(layout, scrollTop, viewportHeight, overscan = 500) {
  const rows = layout.rows;
  let low = 0, high = rows.length;
  const from = Math.max(0, scrollTop - overscan), to = scrollTop + viewportHeight + overscan;
  while (low < high) {
    const middle = (low + high) >>> 1;
    if (rows[middle].top + rows[middle].height < from) low = middle + 1;
    else high = middle;
  }
  const start = low;
  let end = start, tiles = 0;
  while (end < rows.length && rows[end].top <= to) {
    if (tiles + rows[end].tiles.length > 300) break;
    tiles += rows[end].tiles.length;
    end++;
  }
  return {start, end, rows:rows.slice(start,end), tiles, top:rows[start]?.top ?? layout.height, bottom:Math.max(0,layout.height - (rows[end]?.top ?? layout.height))};
}
