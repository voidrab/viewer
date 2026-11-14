// Thin wrapper over the Wails runtime: the Go viewer service's methods and
// events, with the runtime injected so tests can supply a fake. Every window
// receives every event; Go addresses each to one window by name, and this
// window ignores the rest.

const SERVICE = 'viewer/internal/viewer.Service.';

// Event names emitted by Go: viewer.EventOpenFiles, EventShow and EventError.
const EVENT_OPEN_FILES = 'files:open';
const EVENT_SHOW = 'document:show';
const EVENT_ERROR = 'document:error';
// viewer.EventDrag goes to every window; EventMovedOut to the drag's source.
const EVENT_DRAG = 'tabs:drag';
const EVENT_MOVED_OUT = 'tabs:moved-out';
const EVENT_DRAG_CANCELLED = 'tabs:drag-cancelled';

/**
 * @param {object} runtime the @wailsio/runtime module, or a fake
 */
export function createApi({ Call, Events, Browser, System, Window }) {
  const call = (method, ...args) => Call.ByName(SERVICE + method, ...args);
  const self = Window.Name();
  const forThisWindow = (cb) => async (e) => {
    if (e.data.window === (await self)) cb(e.data.data);
  };
  const forEveryWindow = (cb) => (e) => cb(e.data.data);
  return {
    initialFiles: () => call('InitialFiles'),
    profiles: () => call('Profiles'),
    pickFiles: () => call('PickFiles'),
    open: (path) => call('Open', path),
    release: (id) => call('Release', id),
    newWindow: () => call('NewWindow'),
    // Tab drags between windows: the drag's data goes through Go, which
    // tells every window about it and hands the tabs to the one dropped on.
    startDrag: (items) => call('StartDrag', items),
    endDrag: (token, detach) => call('EndDrag', token, detach),
    initialTabs: () => call('InitialTabs'),
    drop: (token) => call('Drop', token),
    // verify and convert act on a document by ID, and return cancellable
    // promises: .cancel() stops the work on the Go side through its context.
    verify: (id, profile) => call('Verify', id, profile),
    convert: (id, profile) => call('Convert', id, profile),
    openURL: (url) => Browser.OpenURL(url),
    onOpenFiles: (cb) => Events.On(EVENT_OPEN_FILES, forThisWindow(cb)),
    onShow: (cb) => Events.On(EVENT_SHOW, forThisWindow(cb)),
    onError: (cb) => Events.On(EVENT_ERROR, forThisWindow(cb)),
    onDrag: (cb) => Events.On(EVENT_DRAG, forEveryWindow(cb)),
    onMovedOut: (cb) => Events.On(EVENT_MOVED_OUT, forThisWindow(cb)),
    onDragCancelled: (cb) => Events.On(EVENT_DRAG_CANCELLED, forEveryWindow(cb)),
    focus: () => Window.Focus(),
    setTitle: (title) => Window.SetTitle(title),
    isMac: () => System.IsMac(),
    minimise: () => Window.Minimise(),
    toggleMaximise: () => Window.ToggleMaximise(),
    close: () => Window.Close(),
  };
}

/** Returns a readable message for a rejected call. */
export function errorMessage(err) {
  return err?.message || String(err);
}
