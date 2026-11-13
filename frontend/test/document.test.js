import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { assetBaseFor, createDocumentView, MAX_CANVAS_PIXELS, ZOOM_PRESETS } from '../src/document.js';

// Minimal stand-ins for the pdf.js viewer classes, recording what they get.
function fakeViewerLib() {
  const made = {};
  class EventBus {
    constructor() {
      made.eventBus = this;
      this.on = vi.fn();
      this.dispatch = vi.fn();
    }
  }
  class PDFLinkService {
    constructor(opts) {
      made.linkService = this;
      this.opts = opts;
      this.setViewer = vi.fn();
      this.setDocument = vi.fn();
    }
  }
  class PDFFindController {
    constructor(opts) {
      made.findController = this;
      this.opts = opts;
    }
  }
  class PDFViewer {
    constructor(opts) {
      made.viewer = this;
      this.opts = opts;
      this.currentPageNumber = 1;
      this.pagesCount = 0;
      this.currentScaleValue = 'auto';
      this.setDocument = vi.fn();
      this.nextPage = vi.fn(() => true);
      this.previousPage = vi.fn(() => false);
      this.increaseScale = vi.fn();
      this.decreaseScale = vi.fn();
    }
  }
  return { made, lib: { EventBus, PDFLinkService, PDFFindController, PDFViewer, LinkTarget: { BLANK: 2 } } };
}

function loadingTask(result) {
  return { promise: result, destroy: vi.fn() };
}

let made;
let pdfjs;
let view;
let container;
let resized; // the ResizeObserver callback the view registered
let frames; // pending requestAnimationFrame callbacks

beforeEach(() => {
  resized = null;
  frames = [];
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(cb) {
        resized = cb;
      }
      observe(el) {
        this.el = el;
      }
    },
  );
  vi.stubGlobal('requestAnimationFrame', (cb) => frames.push(cb));
  vi.stubGlobal('cancelAnimationFrame', (id) => (frames[id - 1] = () => {}));
  const fake = fakeViewerLib();
  made = fake.made;
  pdfjs = { getDocument: vi.fn() };
  container = document.createElement('div');
  view = createDocumentView({ container, pdfjs, viewerLib: fake.lib, assetBase: './pdfjs/' });
});

afterEach(() => vi.unstubAllGlobals());

describe('assetBaseFor', () => {
  it('serves pdf.js data from node_modules in dev and next to the bundle otherwise', () => {
    expect(assetBaseFor({ DEV: true })).toBe('/node_modules/pdfjs-dist/');
    expect(assetBaseFor({ DEV: false })).toBe('./pdfjs/');
  });
});

describe('ZOOM_PRESETS', () => {
  it('starts with fit width and lists unique values', () => {
    expect(ZOOM_PRESETS[0][0]).toBe('page-width');
    expect(new Set(ZOOM_PRESETS.map(([v]) => v)).size).toBe(ZOOM_PRESETS.length);
  });
});

describe('createDocumentView', () => {
  it('wires the pdf.js components together', () => {
    expect(made.viewer.opts).toMatchObject({ container, eventBus: made.eventBus, linkService: made.linkService });
    expect(made.viewer.opts.findController).toBe(made.findController);
    expect(made.linkService.opts).toEqual({ eventBus: made.eventBus, externalLinkTarget: 2 });
    expect(made.findController.opts).toEqual({ eventBus: made.eventBus, linkService: made.linkService });
    expect(made.linkService.setViewer).toHaveBeenCalledWith(made.viewer);
  });

  it('caps canvas size', () => {
    expect(made.viewer.opts.maxCanvasPixels).toBe(MAX_CANVAS_PIXELS);
    expect(MAX_CANVAS_PIXELS).toBeLessThan(2 ** 25); // below pdf.js's default
  });

  it('opens a document by ranges, with the bundled data and no scripting', async () => {
    const pdf = { numPages: 7 };
    const task = loadingTask(Promise.resolve(pdf));
    pdfjs.getDocument.mockReturnValue(task);
    await expect(view.open('/doc/1/a.pdf')).resolves.toEqual({ task, pdf, pages: 7 });
    expect(pdfjs.getDocument).toHaveBeenCalledWith({
      url: '/doc/1/a.pdf',
      disableAutoFetch: true,
      disableStream: true,
      cMapUrl: './pdfjs/cmaps/',
      cMapPacked: true,
      standardFontDataUrl: './pdfjs/standard_fonts/',
      wasmUrl: './pdfjs/wasm/',
      isEvalSupported: false,
      enableScripting: false,
    });
    expect(made.viewer.setDocument).not.toHaveBeenCalled(); // opened, not shown
  });

  it('releases a document that fails to open', async () => {
    const task = loadingTask(Promise.reject(new Error('Invalid PDF structure')));
    pdfjs.getDocument.mockReturnValue(task);
    await expect(view.open('/bad')).rejects.toThrow('Invalid PDF structure');
    expect(task.destroy).toHaveBeenCalledOnce();
  });

  function handle() {
    return { task: { destroy: vi.fn() }, pdf: { cleanup: vi.fn() }, pages: 3 };
  }
  const pagesinit = () => made.eventBus.on.mock.calls.find(([name]) => name === 'pagesinit')[1]();

  it('shows a document and restores its zoom and scroll once laid out', () => {
    const h = handle();
    view.show(h, { scale: '1.5', top: 300, left: 20 });
    expect(made.viewer.setDocument).toHaveBeenCalledWith(h.pdf);
    expect(made.linkService.setDocument).toHaveBeenCalledWith(h.pdf, null);
    pagesinit();
    expect(made.viewer.currentScaleValue).toBe('1.5');
    expect([container.scrollTop, container.scrollLeft]).toEqual([300, 20]);

    // Later layouts (not from show) leave the position alone.
    container.scrollTop = 5;
    pagesinit();
    expect(container.scrollTop).toBe(5);
  });

  it('hides a document, dropping its pages and caches, and reports its position', () => {
    const h = handle();
    view.show(h, { scale: 'page-width', top: 0, left: 0 });
    made.viewer.currentScaleValue = '2';
    container.scrollTop = 120;
    expect(view.hide()).toEqual({ scale: '2', top: 120, left: 0 });
    expect(made.viewer.setDocument).toHaveBeenLastCalledWith(null);
    expect(made.linkService.setDocument).toHaveBeenLastCalledWith(null, null);
    expect(h.pdf.cleanup).toHaveBeenCalledOnce();
  });

  it('closes documents, hiding the shown one first', () => {
    const shown = handle();
    const background = handle();
    view.show(shown, { scale: 'page-width', top: 0, left: 0 });
    view.close(background);
    expect(background.task.destroy).toHaveBeenCalledOnce();
    expect(made.viewer.setDocument).toHaveBeenCalledTimes(1);
    view.close(shown);
    expect(made.viewer.setDocument).toHaveBeenLastCalledWith(null);
    expect(shown.task.destroy).toHaveBeenCalledOnce();
  });

  it('exposes navigation and zoom', () => {
    made.viewer.currentPageNumber = 4;
    made.viewer.pagesCount = 9;
    expect(view.page).toBe(4);
    expect(view.pageCount).toBe(9);

    view.goToPage(6);
    expect(made.viewer.currentPageNumber).toBe(6);
    expect(view.next()).toBe(true);
    expect(view.previous()).toBe(false);
    view.zoomIn();
    view.zoomOut();
    expect(made.viewer.increaseScale).toHaveBeenCalled();
    expect(made.viewer.decreaseScale).toHaveBeenCalled();
    view.setZoom('page-fit');
    expect(made.viewer.currentScaleValue).toBe('page-fit');
  });

  it('dispatches find requests', () => {
    view.find('habitat');
    view.find('habitat', { again: true, previous: true });
    const [[, first], [, again]] = made.eventBus.dispatch.mock.calls;
    expect(made.eventBus.dispatch.mock.calls[0][0]).toBe('find');
    expect(first).toMatchObject({ type: '', query: 'habitat', findPrevious: false, highlightAll: true });
    expect(again).toMatchObject({ type: 'again', findPrevious: true });
  });

  it('refits a fit preset when the container resizes, once per frame', () => {
    made.viewer.pagesCount = 3;
    made.viewer.currentScaleValue = 'page-width';
    const setter = vi.fn();
    let value = 'page-width';
    Object.defineProperty(made.viewer, 'currentScaleValue', { get: () => value, set: (v) => setter((value = v)) });

    resized();
    resized(); // a burst of resizes collapses into one refit
    frames.forEach((f) => f());
    expect(setter).toHaveBeenCalledOnce();
    expect(setter).toHaveBeenCalledWith('page-width');
  });

  it('keeps explicit zoom levels and ignores resizes before a document', () => {
    const setter = vi.fn();
    let value = '1.5';
    Object.defineProperty(made.viewer, 'currentScaleValue', { get: () => value, set: (v) => setter((value = v)) });
    made.viewer.pagesCount = 3;
    resized();
    frames.forEach((f) => f());

    value = 'page-fit';
    made.viewer.pagesCount = 0;
    resized();
    frames.forEach((f) => f());
    expect(setter).not.toHaveBeenCalled();
  });

  it('subscribes to viewer events', () => {
    const cb = () => {};
    view.on('pagechanging', cb);
    expect(made.eventBus.on).toHaveBeenCalledWith('pagechanging', cb);
  });
});
