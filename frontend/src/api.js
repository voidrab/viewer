// Thin wrapper over the Wails runtime: the Go viewer service's methods and
// events, with the runtime injected so tests can supply a fake. Every window
// receives every event; Go addresses each to one window by name, and this
// window ignores the rest.

const SERVICE = 'viewer/internal/viewer.Service.';

// Event names emitted by Go: viewer.EventOpenFiles, EventShow and EventError.
const EVENT_OPEN_FILES = 'files:open';
const EVENT_SHOW = 'document:show';
const EVENT_ERROR = 'document:error';

/**
 * @param {object} runtime the @wailsio/runtime module, or a fake
 */
export function createApi({ Call, Events, Browser, System, Window }) {
  const call = (method, ...args) => Call.ByName(SERVICE + method, ...args);
  const self = Window.Name();
  const forThisWindow = (cb) => async (e) => {
    if (e.data.window === (await self)) cb(e.data.data);
  };
  return {
    initialFiles: () => call('InitialFiles'),
    profiles: () => call('Profiles'),
    pickFiles: () => call('PickFiles'),
    open: (path) => call('Open', path),
    release: (id) => call('Release', id),
    newWindow: () => call('NewWindow'),
    // verify and convert act on a document by ID, and return cancellable
    // promises: .cancel() stops the work on the Go side through its context.
    verify: (id, profile) => call('Verify', id, profile),
    convert: (id, profile) => call('Convert', id, profile),
    openURL: (url) => Browser.OpenURL(url),
    onOpenFiles: (cb) => Events.On(EVENT_OPEN_FILES, forThisWindow(cb)),
    onShow: (cb) => Events.On(EVENT_SHOW, forThisWindow(cb)),
    onError: (cb) => Events.On(EVENT_ERROR, forThisWindow(cb)),
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
