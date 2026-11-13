// Small pure helpers for text the UI shows.

const UNITS = ['KB', 'MB', 'GB'];

/** Formats a byte count, e.g. 1536 -> "1.5 KB". */
export function formatBytes(n) {
  if (n < 1024) return `${n} B`;
  let value = n;
  let unit = -1;
  do {
    value /= 1024;
    unit++;
  } while (value >= 1024 && unit < UNITS.length - 1);
  return `${value.toFixed(value < 10 ? 1 : 0)} ${UNITS[unit]}`;
}

/** Returns "1 issue" or "3 issues"; pass many for irregular plurals. */
export function plural(n, word, many = `${word}s`) {
  return `${n} ${n === 1 ? word : many}`;
}

/**
 * Parses a page number typed by the user, clamped to 1..count. Returns null
 * for text that is not a number.
 */
export function parsePage(text, count) {
  const n = Number.parseInt(text, 10);
  if (Number.isNaN(n)) return null;
  return Math.min(Math.max(n, 1), count);
}

/** Returns the last path segment of a file path. */
export function baseName(path) {
  return path.split(/[\\/]/).pop();
}
