import { describe, expect, it } from 'vitest';

import { baseName, formatBytes, parsePage, plural } from '../src/format.js';

describe('formatBytes', () => {
  it.each([
    [0, '0 B'],
    [1023, '1023 B'],
    [1536, '1.5 KB'],
    [20 * 1024, '20 KB'],
    [5.25 * 1024 ** 2, '5.3 MB'],
    [3 * 1024 ** 3, '3.0 GB'],
    [2048 * 1024 ** 3, '2048 GB'],
  ])('%d -> %s', (n, want) => {
    expect(formatBytes(n)).toBe(want);
  });
});

describe('plural', () => {
  it('adds an s except for one', () => {
    expect(plural(0, 'issue')).toBe('0 issues');
    expect(plural(1, 'issue')).toBe('1 issue');
    expect(plural(2, 'check')).toBe('2 checks');
    expect(plural(2, 'pass', 'passes')).toBe('2 passes');
    expect(plural(1, 'pass', 'passes')).toBe('1 pass');
  });
});

describe('parsePage', () => {
  it.each([
    ['3', 10, 3],
    [' 7 ', 10, 7],
    ['0', 10, 1],
    ['-4', 10, 1],
    ['99', 10, 10],
    ['abc', 10, null],
    ['', 10, null],
  ])('%j of %d -> %j', (text, count, want) => {
    expect(parsePage(text, count)).toBe(want);
  });
});

describe('baseName', () => {
  it('handles both separators', () => {
    expect(baseName('/home/a/report.pdf')).toBe('report.pdf');
    expect(baseName('C:\\docs\\x.pdf')).toBe('x.pdf');
    expect(baseName('plain.pdf')).toBe('plain.pdf');
  });
});
