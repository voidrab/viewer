import { describe, expect, it, vi } from 'vitest';

import { createApi, errorMessage } from '../src/api.js';

function fakeRuntime() {
  return {
    Call: { ByName: vi.fn(() => 'result') },
    Events: { On: vi.fn(() => 'unsubscribe') },
    Browser: { OpenURL: vi.fn() },
    System: { IsMac: vi.fn(() => true) },
    Window: {
      Name: vi.fn(async () => 'window-1'),
      SetTitle: vi.fn(),
      Minimise: vi.fn(),
      ToggleMaximise: vi.fn(),
      Close: vi.fn(),
    },
  };
}

describe('createApi', () => {
  it.each([
    ['initialFiles', [], 'InitialFiles'],
    ['profiles', [], 'Profiles'],
    ['pickFiles', [], 'PickFiles'],
    ['open', ['/a.pdf'], 'Open'],
    ['release', [7], 'Release'],
    ['newWindow', [], 'NewWindow'],
    ['verify', [7, 'pdfa1b'], 'Verify'],
    ['convert', [7, 'pdfa2b'], 'Convert'],
  ])('%s calls the Go service method %s', (name, args, method) => {
    const rt = fakeRuntime();
    expect(createApi(rt)[name](...args)).toBe('result');
    expect(rt.Call.ByName).toHaveBeenCalledWith(`viewer/internal/viewer.Service.${method}`, ...args);
  });

  it('handles only the events addressed to this window', async () => {
    const rt = fakeRuntime();
    const api = createApi(rt);
    const opened = vi.fn();
    const shown = vi.fn();
    const failed = vi.fn();
    expect(api.onOpenFiles(opened)).toBe('unsubscribe');
    api.onShow(shown);
    api.onError(failed);

    const [[openName, openCb], [showName, showCb], [errorName, errorCb]] = rt.Events.On.mock.calls;
    // Must match viewer.EventOpenFiles, EventShow and EventError in Go.
    expect([openName, showName, errorName]).toEqual(['files:open', 'document:show', 'document:error']);
    await openCb({ data: { window: 'window-2', data: ['/other.pdf'] } });
    await openCb({ data: { window: 'window-1', data: ['/a.pdf'] } });
    await showCb({ data: { window: 'window-1', data: 3 } });
    await errorCb({ data: { window: 'window-1', data: 'nope' } });
    expect(opened).toHaveBeenCalledExactlyOnceWith(['/a.pdf']);
    expect(shown).toHaveBeenCalledExactlyOnceWith(3);
    expect(failed).toHaveBeenCalledExactlyOnceWith('nope');
  });

  it('forwards browser, platform and window calls', () => {
    const rt = fakeRuntime();
    const api = createApi(rt);
    api.openURL('https://go.dev');
    expect(rt.Browser.OpenURL).toHaveBeenCalledWith('https://go.dev');
    expect(api.isMac()).toBe(true);
    api.setTitle('a.pdf – Viewer');
    expect(rt.Window.SetTitle).toHaveBeenCalledWith('a.pdf – Viewer');
    api.minimise();
    api.toggleMaximise();
    api.close();
    expect(rt.Window.Minimise).toHaveBeenCalled();
    expect(rt.Window.ToggleMaximise).toHaveBeenCalled();
    expect(rt.Window.Close).toHaveBeenCalled();
  });
});

describe('errorMessage', () => {
  it('prefers the error message and falls back to the value', () => {
    expect(errorMessage(new Error('boom'))).toBe('boom');
    expect(errorMessage('plain')).toBe('plain');
    expect(errorMessage(null)).toBe('null');
  });
});
