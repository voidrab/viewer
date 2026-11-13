// Shared fixtures: the real index.html markup, and fakes for the Go API and
// the pdf.js view.

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { vi } from 'vitest';

const html = readFileSync(resolve(import.meta.dirname, '../index.html'), 'utf8');

/** Replaces the test document's body with index.html's, minus scripts. */
export function loadPage() {
  const parsed = new DOMParser().parseFromString(html, 'text/html');
  parsed.querySelectorAll('script').forEach((s) => s.remove());
  document.title = '';
  document.body.replaceWith(document.importNode(parsed.body, true));
}

/** Waits for pending promise callbacks to run. */
export const flush = () => new Promise((r) => setTimeout(r, 0));

/** A promise with its resolvers exposed, plus a cancel spy like Wails's. */
export function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  promise.cancel = vi.fn();
  return { promise, resolve, reject };
}

export const profiles = [
  { name: 'pdfa1b', label: 'PDF/A-1b' },
  { name: 'pdfa2b', label: 'PDF/A-2b' },
];

let lastID = 0;

/** A Go viewer.Document; each gets a fresh ID unless one is given. */
export function makeDoc(overrides = {}) {
  const id = overrides.id ?? ++lastID;
  const name = overrides.name ?? (overrides.path ?? '/docs/report.pdf').split('/').pop();
  return {
    id,
    path: `/docs/${name}`,
    name,
    size: 2048,
    url: `/doc/${id}/${name}`,
    info: { version: '1.7', title: '', claimed: '' },
    ...overrides,
  };
}

export function fakeApi(overrides = {}) {
  const handlers = {};
  return {
    handlers,
    initialFiles: vi.fn(async () => []),
    profiles: vi.fn(async () => profiles),
    pickFiles: vi.fn(async () => ['/docs/report.pdf']),
    open: vi.fn(async (path) => makeDoc({ path })),
    release: vi.fn(),
    newWindow: vi.fn(),
    verify: vi.fn(async () => ({ valid: true, total: 0, issues: [], millis: 5 })),
    convert: vi.fn(async () => null),
    setTitle: vi.fn(),
    openURL: vi.fn(),
    onOpenFiles: vi.fn((cb) => (handlers.openFiles = cb)),
    onShow: vi.fn((cb) => (handlers.show = cb)),
    onError: vi.fn((cb) => (handlers.error = cb)),
    isMac: vi.fn(() => false),
    minimise: vi.fn(),
    toggleMaximise: vi.fn(),
    close: vi.fn(),
    ...overrides,
  };
}

export function fakeView(overrides = {}) {
  const handlers = {};
  return {
    handlers,
    emit: (name, data) => handlers[name]?.(data),
    open: vi.fn(async (url) => ({ url, pages: 3 })),
    show: vi.fn(),
    hide: vi.fn(() => ({ scale: '1.5', top: 40, left: 0 })),
    close: vi.fn(),
    page: 1,
    pageCount: 3,
    goToPage: vi.fn(),
    next: vi.fn(),
    previous: vi.fn(),
    zoomIn: vi.fn(),
    zoomOut: vi.fn(),
    setZoom: vi.fn(),
    find: vi.fn(),
    on: vi.fn((name, cb) => (handlers[name] = cb)),
    ...overrides,
  };
}
