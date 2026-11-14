// The app controller: wires the tabs, toolbar, keyboard, find bar and PDF/A
// panel in index.html to the Go service (api) and the pdf.js view.
//
// All tabs share one pdf.js viewer. Only the active tab's document is in it;
// a background tab keeps its parsed document but no rendered pages, and the
// same happens to the active tab while the window is hidden. Beyond the
// KEEP_OPEN most recently used, background tabs close their pdf.js document
// too, keeping only their place, and reopen it when switched back to.

import { errorMessage } from './api.js';
import { ZOOM_PRESETS } from './document.js';
import { el } from './dom.js';
import { baseName, formatBytes, parsePage } from './format.js';
import { renderConversion, renderReport } from './report.js';

const TOAST_MS = 4000;
/** How many tabs keep their pdf.js document open, the active one included. */
export const KEEP_OPEN = 3;
const EXTERNAL_LINK = /^(https?|mailto):/i;

/**
 * @param {object} deps
 * @param {Document} deps.document the page, holding index.html's elements
 * @param {ReturnType<import('./api.js').createApi>} deps.api
 * @param {() => Promise<ReturnType<import('./document.js').createDocumentView>>} deps.loadView
 *   creates the pdf.js view; called once, on the first document
 */
export function createApp({ document, api, loadView }) {
  const $ = (id) => document.getElementById(id);
  const ui = {
    body: document.body,
    open: $('open'),
    emptyOpen: $('empty-open'),
    tabs: $('tabs'),
    tabList: $('tab-list'),
    tabNew: $('tab-new'),
    prev: $('prev'),
    next: $('next'),
    page: $('page'),
    pages: $('pages'),
    zoom: $('zoom'),
    zoomIn: $('zoom-in'),
    zoomOut: $('zoom-out'),
    find: $('find'),
    findCount: $('find-count'),
    pdfa: $('pdfa'),
    claimed: $('claimed'),
    container: $('container'),
    panel: $('panel'),
    panelClose: $('panel-close'),
    docName: $('doc-name'),
    docMeta: $('doc-meta'),
    profile: $('profile'),
    verify: $('verify'),
    convert: $('convert'),
    cancel: $('cancel'),
    result: $('result'),
    toast: $('toast'),
    statusFile: $('status-file'),
    statusZoom: $('status-zoom'),
    toolbar: document.querySelector('.toolbar'),
    winMin: $('win-min'),
    winMax: $('win-max'),
    winClose: $('win-close'),
  };

  // Listeners on the document go through this, so dispose can remove them.
  const lifetime = new AbortController();
  const { signal } = lifetime;

  let view = null; // the pdf.js view, once loaded
  let viewReady = null;
  let running = null; // the in-flight verify/convert; the buttons allow one
  let toastTimer = 0;
  let zoomPreset = 'page-width'; // for documents opened from now on

  // ----- feedback -----

  function toast(message) {
    ui.toast.textContent = message;
    ui.toast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {
      ui.toast.hidden = true;
    }, TOAST_MS);
  }
  const fail = (err) => toast(errorMessage(err));

  // ----- the pdf.js view, loaded on first use -----

  function getView() {
    viewReady ??= loadView().then((v) => {
      view = v;
      v.on('pagechanging', ({ pageNumber }) => {
        ui.page.value = String(pageNumber);
        ui.prev.disabled = pageNumber <= 1;
        ui.next.disabled = pageNumber >= v.pageCount;
      });
      v.on('scalechanging', syncZoom);
      v.on('updatefindmatchescount', syncFind);
      v.on('updatefindcontrolstate', syncFind);
      return v;
    });
    return viewReady;
  }

  // ----- tabs -----

  /**
   * A tab: doc is the Go viewer.Document, null while empty; handle is its
   * opened pdf.js document, null while closed to save memory; state is where
   * it was scrolled to while in the background; result is its last PDF/A
   * report; used orders tabs by when they were last active.
   */
  const tabs = [];
  let active = null;
  let shown = null; // the tab whose document is in the viewer
  let lastKey = 0;
  let lastUse = 0;

  function newTab() {
    const tab = {
      key: ++lastKey,
      doc: null,
      handle: null,
      pages: 0,
      label: '',
      loading: false,
      state: null,
      result: null,
      used: 0,
    };
    tabs.push(tab);
    return tab;
  }

  /** Puts the active tab's document in the viewer, unless it is there. */
  function showActive() {
    if (!active.handle || shown === active || document.hidden) return;
    view.show(active.handle, active.state ?? { scale: zoomPreset, top: 0, left: 0 });
    shown = active;
  }

  /** Closes the pdf.js documents of all but the KEEP_OPEN most recent tabs. */
  function trim() {
    const open = tabs.filter((t) => t.handle).sort((a, b) => b.used - a.used);
    for (const tab of open.slice(KEEP_OPEN)) {
      view.close(tab.handle);
      tab.handle = null;
    }
  }

  /** Reopens the pdf.js document of a tab that was trimmed. */
  async function reopen(tab) {
    Object.assign(tab, { loading: true, label: tab.doc.name });
    present();
    try {
      const handle = await view.open(tab.doc.url);
      if (tabs.includes(tab)) tab.handle = handle;
      else view.close(handle);
    } catch (err) {
      toast(`Could not reopen ${tab.doc.name}: ${errorMessage(err)}`);
    }
    tab.loading = false;
    if (tab === active) showActive();
    trim();
    if (tab === active) present();
  }

  /** Takes the shown document out of the viewer, keeping its position. */
  function hideShown() {
    if (!shown) return;
    shown.state = view.hide();
    shown = null;
  }

  function activate(tab) {
    if (shown !== tab) hideShown();
    active = tab;
    tab.used = ++lastUse;
    if (tab.doc && !tab.handle && !tab.loading) {
      reopen(tab);
      return;
    }
    showActive();
    trim();
    present();
  }

  function closeTab(tab) {
    const i = tabs.indexOf(tab);
    tabs.splice(i, 1);
    if (tab.handle) {
      if (shown === tab) shown = null;
      view.close(tab.handle);
    }
    if (tab.doc) api.release(tab.doc.id);
    if (!tabs.length) {
      api.close(); // the window's last tab
    } else if (tab === active) {
      activate(tabs[Math.min(i, tabs.length - 1)]);
    } else {
      renderTabs();
    }
  }

  function step(delta) {
    activate(tabs[(tabs.indexOf(active) + delta + tabs.length) % tabs.length]);
  }

  function renderTabs() {
    // A lone empty tab needs no bar; a single open document gets its tab.
    ui.tabs.hidden = tabs.length < 2 && !tabs[0]?.doc && !tabs[0]?.loading;
    ui.tabList.replaceChildren(
      ...tabs.map((tab) =>
        el(
          'div',
          {
            className: tab === active ? 'tab active' : 'tab',
            role: 'tab',
            title: tab.doc?.path ?? '',
            'data-tab': tab.key,
            'aria-selected': String(tab === active),
          },
          el('span', { className: 'tab-title' }, tab.doc?.name ?? (tab.loading ? `${tab.label}…` : 'new tab')),
          el('button', { type: 'button', className: 'tab-close', title: 'Close tab (Ctrl+W)', 'data-close': tab.key }, '×'),
        ),
      ),
    );
  }

  /** Shows the active tab's state in the toolbar, status line and panel. */
  function present() {
    const { doc, pages, loading } = active;
    ui.body.classList.toggle('no-doc', !doc);
    ui.body.classList.toggle('loading', loading);
    renderTabs();
    api.setTitle(doc ? `${doc.info.title || doc.name} – Viewer` : 'Viewer');
    ui.result.replaceChildren(...(active.result ? [active.result] : []));
    ui.find.value = '';
    ui.findCount.textContent = '';
    if (loading) {
      ui.statusFile.textContent = `loading ${active.label}…`;
    } else if (!doc) {
      ui.statusFile.textContent = '';
    } else {
      const facts = [`${pages} pages`, formatBytes(doc.size), doc.info.version && `pdf ${doc.info.version}`].filter(
        Boolean,
      );
      ui.statusFile.textContent = [doc.name, ...facts].join('  ·  ');
      ui.pages.textContent = `/ ${pages}`;
      ui.claimed.textContent = doc.info.claimed;
      ui.claimed.hidden = !doc.info.claimed;
      ui.docName.textContent = doc.name;
      ui.docMeta.textContent = [...facts, doc.info.claimed && `claims ${doc.info.claimed}`].filter(Boolean).join(' · ');
    }
  }

  // ----- opening documents -----

  /** Opens each file in a tab: the first in the active tab if it is empty. */
  async function openFiles(paths) {
    const jobs = paths.map((path, i) => {
      const reuse = i === 0 && !active.doc && !active.loading;
      const tab = reuse ? active : newTab();
      Object.assign(tab, { loading: true, label: baseName(path) });
      return [tab, path];
    });
    if (!jobs.length) return;
    activate(jobs[0][0]);
    // Start loading pdf.js while Go reads the first file.
    getView();
    for (const [tab, path] of jobs) await load(tab, path);
  }

  async function load(tab, path) {
    try {
      const doc = await api.open(path);
      const v = await getView();
      let handle;
      try {
        handle = await v.open(doc.url);
      } catch (err) {
        api.release(doc.id);
        throw err;
      }
      if (!tabs.includes(tab)) {
        // Closed while it loaded.
        v.close(handle);
        api.release(doc.id);
        return;
      }
      Object.assign(tab, { doc, handle, pages: handle.pages, loading: false });
      if (doc.note) toast(doc.note);
    } catch (err) {
      tab.loading = false;
      toast(`Could not open ${baseName(path)}: ${errorMessage(err)}`);
      if (!tabs.includes(tab)) return;
      if (tabs.length > 1) {
        closeTab(tab);
        return;
      }
    }
    if (tab === active) showActive();
    trim();
    if (tab === active) present();
    else renderTabs();
  }

  const pickFiles = () => api.pickFiles().then(openFiles, fail);

  // ----- navigation and zoom -----

  function goToTyped() {
    const n = parsePage(ui.page.value, view.pageCount);
    if (n === null) ui.page.value = String(view.page);
    else view.goToPage(n);
  }

  function setZoom(value) {
    zoomPreset = value;
    view.setZoom(value);
  }

  // Reflects the viewer's scale in the zoom menu; a scale no preset matches
  // shows as a percentage in the "custom" entry.
  function syncZoom({ scale, presetValue }) {
    const percent = `${Math.round(scale * 100)}%`;
    ui.statusZoom.textContent = percent;
    const custom = ui.zoom.querySelector('option[value="custom"]');
    const preset = presetValue ?? String(scale);
    if (ZOOM_PRESETS.some(([v]) => v === preset)) {
      custom.hidden = true;
      ui.zoom.value = preset;
    } else {
      custom.hidden = false;
      custom.textContent = percent;
      ui.zoom.value = 'custom';
    }
  }

  // ----- find -----

  function search(query) {
    if (!query) ui.findCount.textContent = '';
    view.find(query);
  }

  function syncFind({ matchesCount: { current: at, total } }) {
    if (!ui.find.value) ui.findCount.textContent = '';
    else if (!total) ui.findCount.textContent = 'No matches';
    else ui.findCount.textContent = `${at} / ${total}`;
  }

  // ----- PDF/A -----

  const profileLabel = () => ui.profile.selectedOptions[0]?.textContent ?? '';

  function setBusy(text) {
    ui.body.classList.toggle('busy', Boolean(text));
    ui.verify.disabled = ui.convert.disabled = ui.profile.disabled = Boolean(text);
    ui.cancel.hidden = !text;
    if (text) ui.result.replaceChildren(el('p', { className: 'meta' }, text));
  }

  /** Runs a verify or convert on the active tab's document; its report stays with that tab. */
  async function runCheck(kind) {
    const tab = active;
    const label = profileLabel();
    const promise = api[kind](tab.doc.id, ui.profile.value);
    running = promise;
    setBusy(kind === 'verify' ? `Checking against ${label}…` : `Converting to ${label}…`);
    try {
      const result = await promise;
      tab.result = result && (kind === 'verify' ? renderReport(result, label) : renderConversion(result, label));
    } catch (err) {
      tab.result = el('p', { className: 'verdict bad' }, errorMessage(err));
    } finally {
      running = null;
      setBusy('');
    }
    if (tab === active) ui.result.replaceChildren(...(tab.result ? [tab.result] : []));
  }

  function togglePanel(open = ui.panel.hidden) {
    ui.panel.hidden = !open;
    ui.pdfa.setAttribute('aria-pressed', String(open));
  }

  // ----- wiring -----

  ui.open.addEventListener('click', pickFiles);
  ui.emptyOpen.addEventListener('click', pickFiles);
  ui.tabNew.addEventListener('click', () => activate(newTab()));
  ui.tabList.addEventListener('click', (e) => {
    const target = e.target.closest('[data-close],[data-tab]');
    if (!target) return;
    const key = Number(target.dataset.close ?? target.dataset.tab);
    const tab = tabs.find((t) => t.key === key);
    if (target.dataset.close) closeTab(tab);
    else activate(tab);
  });
  // Middle-click closes a tab, as in browsers.
  ui.tabList.addEventListener('auxclick', (e) => {
    const target = e.target.closest('[data-tab]');
    if (e.button === 1 && target) closeTab(tabs.find((t) => t.key === Number(target.dataset.tab)));
  });
  ui.prev.addEventListener('click', () => view.previous());
  ui.next.addEventListener('click', () => view.next());
  ui.page.addEventListener('change', goToTyped);
  ui.page.addEventListener('focus', () => ui.page.select());
  ui.zoomIn.addEventListener('click', () => view.zoomIn());
  ui.zoomOut.addEventListener('click', () => view.zoomOut());
  ui.zoom.addEventListener('change', () => setZoom(ui.zoom.value));
  ui.find.addEventListener('input', () => search(ui.find.value));
  ui.find.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') view.find(ui.find.value, { again: true, previous: e.shiftKey });
    else if (e.key === 'Escape') {
      ui.find.value = '';
      search('');
      ui.find.blur();
    }
  });
  ui.pdfa.addEventListener('click', () => togglePanel());
  ui.panelClose.addEventListener('click', () => togglePanel(false));
  ui.verify.addEventListener('click', () => runCheck('verify'));
  ui.convert.addEventListener('click', () => runCheck('convert'));
  ui.cancel.addEventListener('click', () => running?.cancel());
  ui.result.addEventListener('click', (e) => {
    const target = e.target.closest('[data-page],[data-open]');
    if (!target) return;
    if (target.dataset.open) openFiles([target.dataset.open]);
    else view.goToPage(Number(target.dataset.page));
  });

  // Window controls for the frameless window (macOS keeps its own).
  ui.body.classList.toggle('mac', api.isMac());
  ui.winMin.addEventListener('click', () => api.minimise());
  ui.winMax.addEventListener('click', () => api.toggleMaximise());
  ui.winClose.addEventListener('click', () => api.close());
  // Double-clicking the title bar maximises, as native title bars do. "Title
  // bar" is whatever CSS marks draggable, the same test the Wails runtime uses.
  ui.toolbar.addEventListener('dblclick', (e) => {
    const drag = document.defaultView.getComputedStyle(e.target).getPropertyValue('--wails-draggable');
    if (drag.trim() === 'drag') api.toggleMaximise();
  });

  // Links to the web open in the system browser, not inside the app window.
  ui.container.addEventListener('click', (e) => {
    const link = e.target.closest('a[href]');
    if (link && EXTERNAL_LINK.test(link.getAttribute('href'))) {
      e.preventDefault();
      api.openURL(link.href);
    }
  });
  ui.container.addEventListener(
    'wheel',
    (e) => {
      if (!e.ctrlKey) return;
      e.preventDefault();
      if (e.deltaY < 0) view.zoomIn();
      else view.zoomOut();
    },
    { passive: false },
  );

  // A hidden window keeps no rendered pages either.
  document.addEventListener(
    'visibilitychange',
    () => {
      if (document.hidden) hideShown();
      else showActive();
    },
    { signal },
  );

  // Shortcuts with Ctrl (Cmd on macOS) work everywhere; plain keys only when
  // the focus is not in a form field, so typing a page number still works.
  // Document shortcuts need the active tab to show a document.
  const anytimeShortcuts = {
    o: pickFiles,
    n: () => api.newWindow(),
    t: () => activate(newTab()),
    w: () => closeTab(active),
    Tab: (e) => step(e.shiftKey ? -1 : 1),
    PageDown: () => step(1),
    PageUp: () => step(-1),
  };
  const modShortcuts = {
    f: () => {
      ui.find.focus();
      ui.find.select();
    },
    '=': () => view.zoomIn(),
    '+': () => view.zoomIn(),
    '-': () => view.zoomOut(),
    0: () => setZoom('page-width'),
  };
  const plainShortcuts = {
    ArrowLeft: () => view.previous(),
    ArrowRight: () => view.next(),
    Escape: () => togglePanel(false),
  };

  document.addEventListener(
    'keydown',
    (e) => {
      const mod = e.ctrlKey || e.metaKey;
      const typing = Boolean(e.target.closest?.('input, select, textarea'));
      const docShortcuts = !active.doc ? {} : mod ? modShortcuts : typing ? {} : plainShortcuts;
      const action = (mod && anytimeShortcuts[e.key]) || docShortcuts[e.key];
      if (!action) return;
      e.preventDefault();
      action(e);
    },
    { signal },
  );

  api.onOpenFiles(openFiles);
  api.onShow((id) => {
    const tab = tabs.find((t) => t.doc?.id === id);
    if (tab) activate(tab);
  });
  api.onError(toast);

  // ----- startup -----

  ui.zoom.replaceChildren(
    ...ZOOM_PRESETS.map(([value, text]) => el('option', { value }, text)),
    el('option', { value: 'custom', hidden: true }),
  );

  active = newTab();
  // A window opened for files shows its loading bar from first paint (see
  // index.html); keep it until they load.
  if (!ui.body.classList.contains('loading')) present();

  async function start() {
    try {
      const [profiles, files] = await Promise.all([api.profiles(), api.initialFiles()]);
      ui.profile.replaceChildren(...profiles.map((p) => el('option', { value: p.name }, p.label)));
      if (files.length) await openFiles(files);
      else present();
    } catch (err) {
      present();
      fail(err);
    }
  }

  return {
    ready: start(),
    /** Removes the app's document-level listeners. */
    dispose: () => lifetime.abort(),
    get tabs() {
      return tabs;
    },
    get active() {
      return active;
    },
  };
}
