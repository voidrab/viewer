import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { createApp, KEEP_OPEN } from '../src/app.js';
import { deferred, fakeApi, fakeView, flush, loadPage, makeDoc } from './helpers.js';

const $ = (id) => document.getElementById(id);
const click = (id) => $(id).click();
const key = (init, target = document) =>
  target.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init }));
const tabEls = () => [...document.querySelectorAll('#tab-list .tab')];
const tabTitles = () => tabEls().map((t) => t.querySelector('.tab-title').textContent);

let api;
let view;
let loadView;
let app;

async function start(apiOverrides = {}, viewOverrides = {}) {
  api = fakeApi(apiOverrides);
  view = fakeView(viewOverrides);
  loadView = vi.fn(async () => view);
  app = createApp({ document, api, loadView });
  await app.ready;
}

/** Starts the app and opens the given files, waiting for them to load. */
async function startWith(...paths) {
  await start();
  await api.handlers.openFiles(paths);
}

function setHidden(hidden) {
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden });
  document.dispatchEvent(new Event('visibilitychange'));
}

beforeEach(loadPage);
afterEach(() => {
  app?.dispose();
  vi.useRealTimers();
  delete document.hidden;
});

describe('startup', () => {
  it('fills the menus and shows one empty tab without loading pdf.js', async () => {
    await start();
    expect([...$('profile').options].map((o) => [o.value, o.textContent])).toEqual([
      ['pdfa1b', 'PDF/A-1b'],
      ['pdfa2b', 'PDF/A-2b'],
    ]);
    expect($('zoom').options[0].value).toBe('page-width');
    expect($('zoom').querySelector('[value="custom"]').hidden).toBe(true);
    expect(document.body.classList.contains('no-doc')).toBe(true);
    expect($('tabs').hidden).toBe(true);
    expect(app.tabs).toHaveLength(1);
    expect(api.setTitle).toHaveBeenLastCalledWith('Viewer');
    expect(loadView).not.toHaveBeenCalled();
  });

  it('opens the files the window was launched for, in tabs', async () => {
    await start({ initialFiles: vi.fn(async () => ['/docs/a.pdf', '/docs/b.pdf']) });
    expect(tabTitles()).toEqual(['a.pdf', 'b.pdf']);
    expect(app.active.doc.name).toBe('a.pdf');
    expect(loadView).toHaveBeenCalledOnce();
  });

  it('keeps the first-paint loading bar of a window opened for files', async () => {
    document.body.classList.add('loading'); // set by index.html for ?opening
    const files = deferred();
    api = fakeApi({ initialFiles: vi.fn(() => files.promise) });
    view = fakeView();
    app = createApp({ document, api, loadView: async () => view });
    expect(document.body.classList.contains('loading')).toBe(true);
    files.resolve(['/docs/a.pdf']);
    await app.ready;
    expect(document.body.classList.contains('loading')).toBe(false);
    expect(app.active.doc.name).toBe('a.pdf');
  });

  it('clears the loading bar when there turn out to be no files', async () => {
    document.body.classList.add('loading');
    await start();
    expect(document.body.classList.contains('loading')).toBe(false);
  });

  it('reports startup failures', async () => {
    document.body.classList.add('loading');
    await start({ profiles: vi.fn(async () => Promise.reject(new Error('bridge down'))) });
    expect($('toast').textContent).toBe('bridge down');
    expect(document.body.classList.contains('loading')).toBe(false);
  });

  it('marks macOS so the native traffic lights replace the window controls', async () => {
    await start({ isMac: vi.fn(() => true) });
    expect(document.body.classList.contains('mac')).toBe(true);
  });
});

describe('opening documents', () => {
  it('shows a document and its details', async () => {
    await start({
      open: vi.fn(async () => makeDoc({ name: 'report.pdf', info: { title: 'Annual report', version: '1.7', claimed: 'PDF/A-1b' } })),
    });
    await api.handlers.openFiles(['/docs/report.pdf']);
    expect(view.open).toHaveBeenCalledWith(app.active.doc.url);
    expect(view.show).toHaveBeenCalledWith(app.active.handle, { scale: 'page-width', top: 0, left: 0 });
    expect(api.setTitle).toHaveBeenLastCalledWith('Annual report – Viewer');
    expect(document.body.classList.contains('no-doc')).toBe(false);
    expect(document.body.classList.contains('loading')).toBe(false);
    expect($('pages').textContent).toBe('/ 3');
    expect($('claimed').hidden).toBe(false);
    expect($('claimed').textContent).toBe('PDF/A-1b');
    expect($('doc-name').textContent).toBe('report.pdf');
    expect($('doc-meta').textContent).toBe('3 pages · 2.0 KB · pdf 1.7 · claims PDF/A-1b');
    expect($('status-file').textContent).toBe('report.pdf  ·  3 pages  ·  2.0 KB  ·  pdf 1.7');
  });

  it('falls back to the file name and omits unknown details', async () => {
    await start({ open: vi.fn(async () => makeDoc({ name: 'plain.pdf', info: { title: '', version: '', claimed: '' } })) });
    await api.handlers.openFiles(['/docs/plain.pdf']);
    expect(api.setTitle).toHaveBeenLastCalledWith('plain.pdf – Viewer');
    expect($('claimed').hidden).toBe(true);
    expect($('doc-meta').textContent).toBe('3 pages · 2.0 KB');
  });

  it('shows loading progress in the tab and status line', async () => {
    await start();
    const pending = deferred();
    api.open.mockReturnValueOnce(pending.promise);
    const opened = api.handlers.openFiles(['/docs/big.pdf']);
    expect(document.body.classList.contains('loading')).toBe(true);
    expect($('status-file').textContent).toBe('loading big.pdf…');
    pending.resolve(makeDoc({ name: 'big.pdf' }));
    await opened;
    expect(document.body.classList.contains('loading')).toBe(false);
  });

  it('labels tabs while they load', async () => {
    await startWith('/docs/a.pdf');
    const pending = deferred();
    api.open.mockReturnValueOnce(pending.promise);
    const opened = api.handlers.openFiles(['/docs/b.pdf']);
    expect(tabTitles()).toEqual(['a.pdf', 'b.pdf…']);
    pending.resolve(makeDoc({ name: 'b.pdf' }));
    await opened;
    expect(tabTitles()).toEqual(['a.pdf', 'b.pdf']);
  });

  it('opens from the toolbar, the empty state and Ctrl+O', async () => {
    await start();
    click('open');
    click('empty-open');
    key({ key: 'o', ctrlKey: true });
    await flush();
    await flush();
    expect(api.pickFiles).toHaveBeenCalledTimes(3);
    expect(app.tabs).toHaveLength(3); // the first reused the empty tab
  });

  it('ignores a cancelled dialog', async () => {
    await start({ pickFiles: vi.fn(async () => []) });
    click('open');
    await flush();
    expect(api.open).not.toHaveBeenCalled();
    expect(app.tabs).toHaveLength(1);
  });

  it('reports dialog failures', async () => {
    await start({ pickFiles: vi.fn(() => Promise.reject(new Error('no portal'))) });
    click('open');
    await flush();
    expect($('toast').textContent).toBe('no portal');
  });

  it('shows the note for a file gopdfrab could not read', async () => {
    await start({ open: vi.fn(async () => makeDoc({ note: 'This PDF is too damaged to read its structure.' })) });
    await api.handlers.openFiles(['/docs/x.pdf']);
    expect($('toast').textContent).toContain('too damaged');
  });

  it('keeps the only tab, empty, when its file fails to open', async () => {
    await start({ open: vi.fn(() => Promise.reject(new Error('This file is not a PDF document.'))) });
    await api.handlers.openFiles(['/docs/notes.pdf']);
    expect($('toast').textContent).toBe('Could not open notes.pdf: This file is not a PDF document.');
    expect(app.tabs).toHaveLength(1);
    expect(document.body.classList.contains('loading')).toBe(false);
    expect(document.body.classList.contains('no-doc')).toBe(true);
  });

  it('closes a new tab whose file fails to render, and releases it', async () => {
    await startWith('/docs/a.pdf');
    view.open.mockRejectedValueOnce(new Error('Invalid PDF structure.'));
    await api.handlers.openFiles(['/docs/broken.pdf']);
    expect($('toast').textContent).toBe('Could not open broken.pdf: Invalid PDF structure.');
    expect(app.tabs).toHaveLength(1);
    expect(app.active.doc.name).toBe('a.pdf');
    expect(api.release).toHaveBeenCalledOnce();
  });

  it('discards a document whose tab closed while it loaded', async () => {
    await startWith('/docs/a.pdf');
    const pending = deferred();
    api.open.mockReturnValueOnce(pending.promise);
    const opened = api.handlers.openFiles(['/docs/b.pdf']);
    key({ key: 'w', ctrlKey: true }); // closes the loading tab
    const doc = makeDoc({ name: 'b.pdf' });
    pending.resolve(doc);
    await opened;
    expect(view.close).toHaveBeenCalledWith({ url: doc.url, pages: 3 });
    expect(api.release).toHaveBeenCalledWith(doc.id);
    expect(app.tabs).toHaveLength(1);
  });

  it('drops the failure of a tab closed while it loaded', async () => {
    await startWith('/docs/a.pdf');
    const pending = deferred();
    api.open.mockReturnValueOnce(pending.promise);
    const opened = api.handlers.openFiles(['/docs/b.pdf']);
    key({ key: 'w', ctrlKey: true });
    pending.reject(new Error('gone'));
    await opened;
    expect(app.tabs).toHaveLength(1);
    expect(app.active.doc.name).toBe('a.pdf');
  });

  it('opens several files as tabs, showing the first', async () => {
    await startWith('/docs/a.pdf', '/docs/b.pdf', '/docs/c.pdf');
    expect(tabTitles()).toEqual(['a.pdf', 'b.pdf', 'c.pdf']);
    expect($('tabs').hidden).toBe(false);
    expect(app.active.doc.name).toBe('a.pdf');
    expect(view.show).toHaveBeenCalledOnce();
  });

  it('ignores an empty file list', async () => {
    await start();
    await api.handlers.openFiles([]);
    expect(app.tabs).toHaveLength(1);
  });

  it('switches to a tab Go says already shows a file', async () => {
    await startWith('/docs/a.pdf', '/docs/b.pdf');
    api.handlers.show(app.tabs[1].doc.id);
    expect(app.active).toBe(app.tabs[1]);
    api.handlers.show(999); // not here
    expect(app.active).toBe(app.tabs[1]);
  });

  it('shows errors pushed by Go', async () => {
    await start();
    api.handlers.error('Only PDF files can be opened.');
    expect($('toast').textContent).toBe('Only PDF files can be opened.');
  });

  it('hides the toast after a while', async () => {
    vi.useFakeTimers();
    await start();
    api.handlers.error('first');
    api.handlers.error('second');
    vi.advanceTimersByTime(3999);
    expect($('toast').hidden).toBe(false);
    vi.advanceTimersByTime(1);
    expect($('toast').hidden).toBe(true);
  });
});

describe('tabs', () => {
  it('keeps only the active document in the viewer and restores positions', async () => {
    await startWith('/docs/a.pdf', '/docs/b.pdf');
    const [a, b] = app.tabs;
    expect(view.show).toHaveBeenLastCalledWith(a.handle, expect.anything());

    tabEls()[1].click();
    expect(view.hide).toHaveBeenCalledOnce();
    expect(view.show).toHaveBeenLastCalledWith(b.handle, { scale: 'page-width', top: 0, left: 0 });
    expect(a.state).toEqual({ scale: '1.5', top: 40, left: 0 });

    tabEls()[0].click();
    expect(view.show).toHaveBeenLastCalledWith(a.handle, { scale: '1.5', top: 40, left: 0 });

    tabEls()[0].click(); // already shown: nothing to swap
    expect(view.show).toHaveBeenCalledTimes(3);
    expect(tabEls()[0].getAttribute('aria-selected')).toBe('true');
  });

  it('opens and switches tabs from the keyboard', async () => {
    await startWith('/docs/a.pdf', '/docs/b.pdf');
    key({ key: 'Tab', ctrlKey: true });
    expect(app.active).toBe(app.tabs[1]);
    key({ key: 'Tab', ctrlKey: true }); // wraps around
    expect(app.active).toBe(app.tabs[0]);
    key({ key: 'Tab', ctrlKey: true, shiftKey: true });
    expect(app.active).toBe(app.tabs[1]);
    key({ key: 'PageUp', ctrlKey: true });
    expect(app.active).toBe(app.tabs[0]);
    key({ key: 'PageDown', ctrlKey: true });
    expect(app.active).toBe(app.tabs[1]);

    key({ key: 't', ctrlKey: true });
    expect(app.tabs).toHaveLength(3);
    expect(app.active.doc).toBeNull();
    expect(tabTitles()[2]).toBe('new tab');
    expect(document.body.classList.contains('no-doc')).toBe(true);
  });

  it('opens an empty tab from the + button and fills it next', async () => {
    await startWith('/docs/a.pdf');
    click('tab-new');
    expect(app.tabs).toHaveLength(2);
    await api.handlers.openFiles(['/docs/b.pdf']);
    expect(tabTitles()).toEqual(['a.pdf', 'b.pdf']);
  });

  it('closes tabs, releasing their documents', async () => {
    await startWith('/docs/a.pdf', '/docs/b.pdf', '/docs/c.pdf');
    const [a, b, c] = app.tabs;

    // Closing a background tab keeps the active one.
    tabEls()[2].querySelector('.tab-close').click();
    expect(view.close).toHaveBeenCalledWith(c.handle);
    expect(api.release).toHaveBeenCalledWith(c.doc.id);
    expect(app.active).toBe(a);

    // Closing the active tab moves to its neighbour.
    tabEls()[1].click();
    key({ key: 'w', metaKey: true });
    expect(view.close).toHaveBeenCalledWith(b.handle);
    expect(app.active).toBe(a);
    expect(view.show).toHaveBeenLastCalledWith(a.handle, expect.anything());
    expect($('tabs').hidden).toBe(true);
  });

  it('closes the window with its last tab', async () => {
    await start();
    key({ key: 'w', ctrlKey: true });
    expect(api.close).toHaveBeenCalledOnce();
  });

  it('closes a tab with a middle click', async () => {
    await startWith('/docs/a.pdf', '/docs/b.pdf');
    const aux = (target, button) =>
      target.dispatchEvent(new MouseEvent('auxclick', { bubbles: true, button }));
    aux(tabEls()[1], 2); // right button: no
    aux($('tab-list'), 1); // not on a tab
    expect(app.tabs).toHaveLength(2);
    aux(tabEls()[1].querySelector('.tab-title'), 1);
    expect(app.tabs).toHaveLength(1);
  });

  it('ignores clicks between tabs', async () => {
    await startWith('/docs/a.pdf', '/docs/b.pdf');
    $('tab-list').click();
    expect(app.active).toBe(app.tabs[0]);
  });

  it('keeps each tab’s PDF/A report', async () => {
    await startWith('/docs/a.pdf', '/docs/b.pdf');
    click('verify');
    await flush();
    expect($('result').querySelector('.verdict.ok')).not.toBeNull();
    tabEls()[1].click();
    expect($('result').textContent).toBe('');
    tabEls()[0].click();
    expect($('result').querySelector('.verdict.ok')).not.toBeNull();
  });

  it('files a report under its tab when it arrives after a switch', async () => {
    await startWith('/docs/a.pdf', '/docs/b.pdf');
    const pending = deferred();
    api.verify.mockReturnValueOnce(pending.promise);
    click('verify');
    tabEls()[1].click();
    pending.resolve({ valid: true, total: 0, issues: [], millis: 1 });
    await flush();
    expect($('result').textContent).toBe('');
    tabEls()[0].click();
    expect($('result').querySelector('.verdict.ok')).not.toBeNull();
  });
});

describe('keeping memory down', () => {
  it(`keeps only the ${KEEP_OPEN} most recent documents open`, async () => {
    await startWith('/docs/a.pdf', '/docs/b.pdf', '/docs/c.pdf', '/docs/d.pdf');
    const [a, b, c, d] = app.tabs;
    expect(app.tabs.filter((t) => t.handle)).toHaveLength(KEEP_OPEN);
    expect(d.handle).toBeNull(); // the least recently used
    expect(view.close).toHaveBeenCalledOnce();
    expect(api.release).not.toHaveBeenCalled(); // Go keeps the document for it
    expect(tabTitles()).toEqual(['a.pdf', 'b.pdf', 'c.pdf', 'd.pdf']);

    // Switching back reopens it, at the position it was left.
    d.state = { scale: '2', top: 10, left: 0 };
    const reopened = deferred();
    view.open.mockReturnValueOnce(reopened.promise);
    tabEls()[3].click();
    expect(view.open).toHaveBeenLastCalledWith(d.doc.url);
    expect(document.body.classList.contains('loading')).toBe(true);
    expect($('status-file').textContent).toBe('loading d.pdf…');
    reopened.resolve({ url: d.doc.url, pages: 3 });
    await flush();
    expect(view.show).toHaveBeenLastCalledWith(d.handle, { scale: '2', top: 10, left: 0 });
    expect(document.body.classList.contains('loading')).toBe(false);
    expect($('pages').textContent).toBe('/ 3');

    // Still only KEEP_OPEN open: a background tab that was never used went.
    expect([a, b, c, d].map((t) => Boolean(t.handle))).toEqual([true, true, false, true]);

    // Reopening c in turn closes the least recently used other one, b.
    tabEls()[2].click();
    await flush();
    expect([a, b, c, d].map((t) => Boolean(t.handle))).toEqual([true, false, true, true]);
  });

  it('closes a trimmed tab without touching the viewer', async () => {
    await startWith('/docs/a.pdf', '/docs/b.pdf', '/docs/c.pdf', '/docs/d.pdf');
    const d = app.tabs[3];
    view.close.mockClear();
    tabEls()[3].querySelector('.tab-close').click();
    expect(view.close).not.toHaveBeenCalled();
    expect(api.release).toHaveBeenCalledWith(d.doc.id);
  });

  it('reports a document that fails to reopen', async () => {
    await startWith('/docs/a.pdf', '/docs/b.pdf', '/docs/c.pdf', '/docs/d.pdf');
    view.open.mockRejectedValueOnce(new Error('file moved'));
    tabEls()[3].click();
    await flush();
    expect($('toast').textContent).toBe('Could not reopen d.pdf: file moved');
    expect(document.body.classList.contains('loading')).toBe(false);
  });

  it('drops a reopen whose tab closed or lost focus meanwhile', async () => {
    await startWith('/docs/a.pdf', '/docs/b.pdf', '/docs/c.pdf', '/docs/d.pdf');
    const d = app.tabs[3];
    const first = deferred();
    view.open.mockReturnValueOnce(first.promise);
    tabEls()[3].click();
    tabEls()[0].click(); // switched away while it reopens
    first.resolve({ url: d.doc.url, pages: 3 });
    await flush();
    expect(app.active).toBe(app.tabs[0]);
    expect(view.show).not.toHaveBeenLastCalledWith(d.handle, expect.anything());

    // Trim it again by visiting the others, then close it mid-reopen.
    for (const i of [1, 2]) {
      tabEls()[i].click();
      await flush();
    }
    expect(d.handle).toBeNull();
    const second = deferred();
    view.open.mockReturnValueOnce(second.promise);
    tabEls()[3].click();
    key({ key: 'w', ctrlKey: true });
    const late = { url: d.doc.url, pages: 3 };
    second.resolve(late);
    await flush();
    expect(view.close).toHaveBeenLastCalledWith(late);
  });
});

describe('window visibility', () => {
  it('drops rendered pages while hidden and restores them when shown', async () => {
    await startWith('/docs/a.pdf');
    const [a] = app.tabs;
    setHidden(true);
    expect(view.hide).toHaveBeenCalledOnce();
    setHidden(true); // nothing shown any more
    expect(view.hide).toHaveBeenCalledOnce();
    setHidden(false);
    expect(view.show).toHaveBeenLastCalledWith(a.handle, { scale: '1.5', top: 40, left: 0 });
  });

  it('does not render documents that load while hidden', async () => {
    await start();
    setHidden(true);
    await api.handlers.openFiles(['/docs/a.pdf']);
    expect(view.show).not.toHaveBeenCalled();
    setHidden(false);
    expect(view.show).toHaveBeenCalledOnce();
  });
});

describe('navigation', () => {
  beforeEach(() => startWith('/docs/a.pdf'));

  it('steps pages with the buttons and arrow keys', () => {
    click('prev');
    click('next');
    key({ key: 'ArrowLeft' });
    key({ key: 'ArrowRight' });
    expect(view.previous).toHaveBeenCalledTimes(2);
    expect(view.next).toHaveBeenCalledTimes(2);
  });

  it('goes to a typed page', () => {
    $('page').value = '2';
    $('page').dispatchEvent(new Event('change'));
    expect(view.goToPage).toHaveBeenCalledWith(2);
  });

  it('restores the page number after invalid input', () => {
    view.page = 3;
    $('page').value = 'abc';
    $('page').dispatchEvent(new Event('change'));
    expect(view.goToPage).not.toHaveBeenCalled();
    expect($('page').value).toBe('3');
  });

  it('selects the page number on focus', () => {
    const select = vi.spyOn($('page'), 'select');
    $('page').dispatchEvent(new Event('focus'));
    expect(select).toHaveBeenCalled();
  });

  it('tracks the current page and disables the ends', () => {
    view.emit('pagechanging', { pageNumber: 1 });
    expect($('page').value).toBe('1');
    expect($('prev').disabled).toBe(true);
    expect($('next').disabled).toBe(false);
    view.emit('pagechanging', { pageNumber: 3 });
    expect($('prev').disabled).toBe(false);
    expect($('next').disabled).toBe(true);
  });

  it('leaves arrow keys alone while typing in a field', () => {
    key({ key: 'ArrowLeft' }, $('page'));
    expect(view.previous).not.toHaveBeenCalled();
  });

  it('ignores unbound keys', () => {
    expect(key({ key: 'x' })).toBe(true); // not cancelled
    expect(key({ key: 'x', ctrlKey: true })).toBe(true);
  });
});

describe('shortcuts before a document is open', () => {
  it('opens a new window with Ctrl+N', async () => {
    await start();
    key({ key: 'n', ctrlKey: true });
    expect(api.newWindow).toHaveBeenCalledOnce();
  });

  it('ignores document shortcuts', async () => {
    await start();
    expect(key({ key: 'ArrowRight' })).toBe(true);
    expect(key({ key: '=', ctrlKey: true })).toBe(true);
  });
});

describe('zoom', () => {
  beforeEach(() => startWith('/docs/a.pdf'));

  it('opens later documents at the zoom last chosen', async () => {
    $('zoom').value = '1.5';
    $('zoom').dispatchEvent(new Event('change'));
    expect(view.setZoom).toHaveBeenLastCalledWith('1.5');
    await api.handlers.openFiles(['/docs/b.pdf']);
    tabEls()[1].click();
    expect(view.show).toHaveBeenLastCalledWith(app.tabs[1].handle, { scale: '1.5', top: 0, left: 0 });
  });

  it('zooms with buttons, keys and Ctrl+wheel', () => {
    click('zoom-in');
    click('zoom-out');
    key({ key: '=', ctrlKey: true });
    key({ key: '+', metaKey: true });
    key({ key: '-', ctrlKey: true });
    key({ key: '0', ctrlKey: true });
    const wheel = ({ deltaY, ctrlKey = false }) => {
      const ev = new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY });
      // happy-dom's WheelEvent drops modifier flags, so set it directly.
      Object.defineProperty(ev, 'ctrlKey', { value: ctrlKey });
      return $('container').dispatchEvent(ev);
    };
    expect(wheel({ deltaY: -1, ctrlKey: true })).toBe(false);
    wheel({ deltaY: 1, ctrlKey: true });
    expect(wheel({ deltaY: 1 })).toBe(true); // plain scrolling is untouched
    expect(view.zoomIn).toHaveBeenCalledTimes(4);
    expect(view.zoomOut).toHaveBeenCalledTimes(3);
    expect(view.setZoom).toHaveBeenLastCalledWith('page-width');
  });

  it('reflects presets and custom scales in the menu and status line', () => {
    const custom = $('zoom').querySelector('[value="custom"]');
    view.emit('scalechanging', { scale: 1.37, presetValue: 'page-fit' });
    expect($('zoom').value).toBe('page-fit');
    expect($('status-zoom').textContent).toBe('137%');
    view.emit('scalechanging', { scale: 2 });
    expect($('zoom').value).toBe('2');
    expect(custom.hidden).toBe(true);
    view.emit('scalechanging', { scale: 1.1 });
    expect($('zoom').value).toBe('custom');
    expect(custom.hidden).toBe(false);
    expect(custom.textContent).toBe('110%');
  });
});

describe('find', () => {
  beforeEach(() => startWith('/docs/a.pdf'));

  it('searches as you type and steps through matches', () => {
    $('find').value = 'habitat';
    $('find').dispatchEvent(new Event('input'));
    expect(view.find).toHaveBeenLastCalledWith('habitat');
    key({ key: 'Enter' }, $('find'));
    expect(view.find).toHaveBeenLastCalledWith('habitat', { again: true, previous: false });
    key({ key: 'Enter', shiftKey: true }, $('find'));
    expect(view.find).toHaveBeenLastCalledWith('habitat', { again: true, previous: true });
  });

  it('shows match counts', () => {
    $('find').value = 'habitat';
    view.emit('updatefindmatchescount', { matchesCount: { current: 2, total: 9 } });
    expect($('find-count').textContent).toBe('2 / 9');
    view.emit('updatefindcontrolstate', { matchesCount: { current: 0, total: 0 } });
    expect($('find-count').textContent).toBe('No matches');
    $('find').value = '';
    view.emit('updatefindmatchescount', { matchesCount: { current: 0, total: 0 } });
    expect($('find-count').textContent).toBe('');
  });

  it('clears the count when the field is emptied', () => {
    $('find-count').textContent = '1 / 2';
    $('find').value = '';
    $('find').dispatchEvent(new Event('input'));
    expect($('find-count').textContent).toBe('');
    expect(view.find).toHaveBeenLastCalledWith('');
  });

  it('focuses with Ctrl+F and clears with Escape', () => {
    const select = vi.spyOn($('find'), 'select');
    key({ key: 'f', ctrlKey: true });
    expect(document.activeElement).toBe($('find'));
    expect(select).toHaveBeenCalled();

    $('find').value = 'x';
    $('find-count').textContent = '1 / 1';
    key({ key: 'Escape' }, $('find'));
    expect($('find').value).toBe('');
    expect($('find-count').textContent).toBe('');
    expect(view.find).toHaveBeenLastCalledWith('');
    expect(document.activeElement).not.toBe($('find'));
  });

  it('ignores other keys in the field', () => {
    key({ key: 'a' }, $('find'));
    expect(view.find).not.toHaveBeenCalled();
  });
});

describe('PDF/A panel', () => {
  beforeEach(() => startWith('/docs/a.pdf'));

  it('toggles with the toolbar button, close button and Escape', () => {
    expect($('panel').hidden).toBe(true);
    click('pdfa');
    expect($('panel').hidden).toBe(false);
    expect($('pdfa').getAttribute('aria-pressed')).toBe('true');
    click('pdfa');
    expect($('panel').hidden).toBe(true);
    click('pdfa');
    click('panel-close');
    expect($('panel').hidden).toBe(true);
    click('pdfa');
    key({ key: 'Escape' });
    expect($('panel').hidden).toBe(true);
    expect($('pdfa').getAttribute('aria-pressed')).toBe('false');
  });

  it('verifies the tab’s document against the selected profile', async () => {
    const pending = deferred();
    api.verify.mockReturnValueOnce(pending.promise);
    $('profile').value = 'pdfa2b';
    click('verify');

    expect(api.verify).toHaveBeenCalledWith(app.active.doc.id, 'pdfa2b');
    expect($('result').textContent).toBe('Checking against PDF/A-2b…');
    expect($('verify').disabled).toBe(true);
    expect($('cancel').hidden).toBe(false);
    expect(document.body.classList.contains('busy')).toBe(true);

    pending.resolve({ valid: true, total: 0, issues: [], millis: 3 });
    await flush();
    expect($('result').querySelector('.verdict.ok').textContent).toBe('✓ Conforms to PDF/A-2b');
    expect($('verify').disabled).toBe(false);
    expect($('cancel').hidden).toBe(true);
    expect(document.body.classList.contains('busy')).toBe(false);
  });

  it('shows verification errors', async () => {
    api.verify.mockRejectedValueOnce(new Error('This PDF is password protected.'));
    click('verify');
    await flush();
    expect($('result').querySelector('.verdict.bad').textContent).toBe('This PDF is password protected.');
  });

  it('cancels a running check', async () => {
    const pending = deferred();
    api.verify.mockReturnValueOnce(pending.promise);
    click('verify');
    click('cancel');
    expect(pending.promise.cancel).toHaveBeenCalled();
    pending.reject(new Error('Cancelled.'));
    await flush();
    expect($('result').textContent).toBe('Cancelled.');
  });

  it('does nothing on cancel when nothing runs', () => {
    expect(() => click('cancel')).not.toThrow();
  });

  it('converts and opens the output in a new tab', async () => {
    api.convert.mockResolvedValueOnce({
      output: '/docs/a.pdfa.pdf',
      iterations: 1,
      report: { valid: true, total: 0, issues: [], millis: 40 },
      rasterized: [],
      lost: [],
    });
    click('convert');
    expect($('result').textContent).toBe('Converting to PDF/A-1b…');
    await flush();
    expect($('result').querySelector('.verdict.ok').textContent).toBe('✓ Converted to PDF/A-1b');

    $('result').querySelector('[data-open]').click();
    await flush();
    await flush();
    expect(api.open).toHaveBeenLastCalledWith('/docs/a.pdfa.pdf');
    expect(tabTitles()).toEqual(['a.pdf', 'a.pdfa.pdf']);
    expect(app.active).toBe(app.tabs[1]);
  });

  it('shows nothing when the save dialog is cancelled', async () => {
    click('convert');
    await flush();
    expect($('result').textContent).toBe('');
  });

  it('navigates to pages listed in a report', async () => {
    api.verify.mockResolvedValueOnce({
      valid: false,
      total: 1,
      issues: [{ clause: '6.3.4/1', description: 'x', count: 1, pages: [2], messages: ['m'] }],
      millis: 1,
    });
    click('verify');
    await flush();
    $('result').querySelector('[data-page]').click();
    expect(view.goToPage).toHaveBeenCalledWith(2);
    $('result').querySelector('summary').click(); // not a link
    expect(view.goToPage).toHaveBeenCalledOnce();
  });

  it('blocks a second check while one runs', () => {
    const pending = deferred();
    api.verify.mockReturnValueOnce(pending.promise);
    click('verify');
    click('verify');
    click('convert');
    expect(api.verify).toHaveBeenCalledOnce();
    expect(api.convert).not.toHaveBeenCalled();
  });

  it('labels a check by profile name when the menu is empty', async () => {
    $('profile').replaceChildren();
    const pending = deferred();
    api.verify.mockReturnValueOnce(pending.promise);
    click('verify');
    expect($('result').textContent).toBe('Checking against …');
    pending.resolve(null);
    await flush();
  });
});

describe('window', () => {
  beforeEach(() => start());

  it('wires the window controls', () => {
    click('win-min');
    click('win-max');
    click('win-close');
    expect(api.minimise).toHaveBeenCalled();
    expect(api.toggleMaximise).toHaveBeenCalledOnce();
    expect(api.close).toHaveBeenCalled();
  });

  it('maximises on a double-click of draggable title bar space only', () => {
    // The stylesheet is not loaded in tests, and happy-dom does not inherit
    // custom properties, so mark each element as the CSS would compute it.
    const toolbar = document.querySelector('.toolbar');
    toolbar.style.setProperty('--wails-draggable', 'drag');
    document.querySelector('.spacer').style.setProperty('--wails-draggable', 'drag');
    $('open').style.setProperty('--wails-draggable', 'no-drag');
    const dbl = (target) => target.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    dbl(toolbar);
    dbl(document.querySelector('.spacer'));
    dbl($('open'));
    expect(api.toggleMaximise).toHaveBeenCalledTimes(2);
  });
});

describe('links', () => {
  beforeEach(() => start());

  function link(href) {
    const a = document.createElement('a');
    a.href = href;
    a.append(document.createElement('span'));
    document.querySelector('.pdfViewer').append(a);
    return a;
  }

  it('opens web and mail links in the system browser', () => {
    const a = link('https://example.com/x');
    const ev = new MouseEvent('click', { bubbles: true, cancelable: true });
    a.firstChild.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
    expect(api.openURL).toHaveBeenCalledWith('https://example.com/x');
    link('mailto:someone@example.com').click();
    expect(api.openURL).toHaveBeenCalledTimes(2);
  });

  it('leaves internal links and plain clicks to pdf.js', () => {
    const ev = new MouseEvent('click', { bubbles: true, cancelable: true });
    link('#page=3').dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(false);
    document.querySelector('.pdfViewer').click();
    expect(api.openURL).not.toHaveBeenCalled();
  });
});
