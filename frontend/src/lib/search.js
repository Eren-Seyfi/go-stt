// Transcript search: Turkish-aware, case- and accent-insensitive matching,
// and match highlighting via the CSS Custom Highlight API (no DOM changes, so
// editable rows keep working).

const foldCache = new Map();

/** Folds one UTF-16 unit: lowercase (Turkish rules), no accents, ı → i. Always 1 unit. */
function foldChar(ch) {
  let f = foldCache.get(ch);
  if (f !== undefined) return f;
  f = ch.toLocaleLowerCase('tr').normalize('NFD').replace(/\p{M}/gu, '');
  if (f === 'ı') f = 'i';
  if (f.length !== 1) f = ch.toLowerCase().length === 1 ? ch.toLowerCase() : ch;
  foldCache.set(ch, f);
  return f;
}

/** Folded copy of s with the same length, so indexes map back to s. */
export function fold(s) {
  let out = '';
  for (let i = 0; i < s.length; i++) out += foldChar(s[i]);
  return out;
}

/** True if text contains the (already folded) query. */
export function matches(text, foldedQuery) {
  return !!text && fold(text).includes(foldedQuery);
}

export const highlightSupported = typeof CSS !== 'undefined' && 'highlights' in CSS && typeof Highlight !== 'undefined';

/** Every match of the folded query inside root, in document order. */
export function findRanges(root, foldedQuery) {
  const ranges = [];
  if (!root || !foldedQuery) return ranges;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode: (n) => (n.parentElement?.closest('[data-search-skip]') ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT),
  });
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = fold(node.data);
    for (let i = text.indexOf(foldedQuery); i !== -1; i = text.indexOf(foldedQuery, i + foldedQuery.length)) {
      const r = new Range();
      r.setStart(node, i);
      r.setEnd(node, i + foldedQuery.length);
      ranges.push(r);
    }
  }
  return ranges;
}

/** Paints all matches, and the current one more strongly. */
export function paintHighlights(ranges, current) {
  if (!highlightSupported) return;
  CSS.highlights.set('search', new Highlight(...ranges));
  if (ranges[current]) CSS.highlights.set('search-current', new Highlight(ranges[current]));
  else CSS.highlights.delete('search-current');
}

export function clearHighlights() {
  if (!highlightSupported) return;
  CSS.highlights.delete('search');
  CSS.highlights.delete('search-current');
}
