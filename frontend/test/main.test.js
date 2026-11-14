import { expect, it, vi } from 'vitest';

import { flush, loadPage } from './helpers.js';

// main.js is the composition root: it starts the app with the Wails runtime
// and loads pdf.js (engine.js) only when the first document opens.

const pdfjs = vi.hoisted(() => ({
  GlobalWorkerOptions: {},
  getDocument: vi.fn(() => ({ promise: Promise.resolve({ numPages: 1 }), destroy: vi.fn() })),
}));
const runtime = vi.hoisted(() => {
  const doc = { id: 1, name: 'a.pdf', path: '/a.pdf', size: 1, url: '/doc/1/a.pdf', info: {} };
  const results = { Profiles: [], InitialFiles: [], InitialTabs: [], Open: doc };
  return {
    Call: { ByName: vi.fn(async (name) => results[name.split('.').pop()]) },
    Events: { On: vi.fn() },
    Browser: { OpenURL: vi.fn() },
    System: { IsMac: vi.fn(() => false) },
    Window: { Name: vi.fn(async () => 'window-1'), SetTitle: vi.fn() },
  };
});
const viewerSawPdfjs = vi.hoisted(() => ({ value: null }));

vi.mock('pdfjs-dist', () => pdfjs);
vi.mock('pdfjs-dist/build/pdf.worker.min.mjs?url', () => ({ default: '/assets/worker.mjs' }));
vi.mock('@wailsio/runtime', () => runtime);
vi.mock('pdfjs-dist/web/pdf_viewer.mjs', () => {
  viewerSawPdfjs.value = globalThis.pdfjsLib;
  class Stub {
    on() {}
    setViewer() {}
    setDocument() {}
  }
  return { EventBus: Stub, PDFLinkService: Stub, PDFFindController: Stub, PDFViewer: Stub, LinkTarget: { BLANK: 2 } };
});

it('boots without pdf.js, then loads it for the first document', async () => {
  loadPage();
  const { app } = await import('../src/main.js');
  await app.ready;

  expect(runtime.Call.ByName).toHaveBeenCalledWith('viewer/internal/viewer.Service.Profiles');
  expect(runtime.Call.ByName).toHaveBeenCalledWith('viewer/internal/viewer.Service.InitialFiles');
  expect(viewerSawPdfjs.value).toBeNull();
  expect(pdfjs.GlobalWorkerOptions.workerSrc).toBeUndefined();

  const openFiles = runtime.Events.On.mock.calls.find(([name]) => name === 'files:open')[1];
  await openFiles({ data: { window: 'window-1', data: ['/a.pdf'] } });
  for (let i = 0; i < 5; i++) await flush();

  expect(pdfjs.GlobalWorkerOptions.workerSrc).toBe('/assets/worker.mjs');
  expect(viewerSawPdfjs.value.getDocument).toBe(pdfjs.getDocument); // set before the viewer loaded
  expect(pdfjs.getDocument).toHaveBeenCalledWith(expect.objectContaining({ url: '/doc/1/a.pdf' }));
  expect(app.active.doc.name).toBe('a.pdf');
});
