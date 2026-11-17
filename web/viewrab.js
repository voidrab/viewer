// Viewrab in the browser, the page's side: it starts the web worker the
// WebAssembly program runs in, hands it the canvas, forwards the canvas's
// events to it and does for it what a worker cannot: the file chooser, the
// clipboard, downloads, the page's title and cursor, and the local storage.
// The program never runs on the page's thread, so rendering does not hold
// the page up.
"use strict";

(() => {
	const status = (text, failed) => {
		const el = document.getElementById("viewrab-status");
		if (!el) return;
		el.textContent = text;
		el.hidden = text === "";
		el.classList.toggle("failed", !!failed);
	};

	// Keys whose browser handling is prevented. The worker cannot answer in
	// time, so the page decides, by the rule internal/ui/platform/web's Key
	// reports keys by: every navigation key, and every character typed
	// without Alt or Super; with Ctrl, only the viewer's shortcuts, so that
	// the browser's, such as reloading, keep working. Ctrl+V is left to the
	// browser too, so that it fires the paste event the text arrives in.
	const namedKeys = new Set(["Enter", "Escape", "Tab", "Backspace", "Delete", "ArrowLeft",
		"ArrowRight", "ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End"]);
	const shortcuts = "acfnoqtw0123456789=-+";
	const altGraph = (e) => !!e.getModifierState?.("AltGraph");
	const prevents = (e) => {
		if (namedKeys.has(e.key)) return true;
		if (e.type !== "keydown" || [...e.key].length !== 1 || /\p{Cc}/u.test(e.key)) return false;
		const ag = altGraph(e);
		if ((e.altKey && !ag) || e.metaKey) return false;
		if (!e.ctrlKey || ag) return true;
		let r = e.key;
		if (r.codePointAt(0) > 127) {
			const m = /^(?:Key|Digit)(.)$/.exec(e.code);
			if (m) r = m[1].toLowerCase();
		}
		if (!e.shiftKey) r = r.toLowerCase();
		return shortcuts.includes(r);
	};

	const mods = (e) => ({
		shift: e.shiftKey, ctrl: e.ctrlKey, alt: e.altKey, meta: e.metaKey, altGraph: altGraph(e),
	});
	// noMods is mods for an event made with no key held, as a touch makes.
	const noMods = mods({});

	const storage = () => {
		const out = {};
		try {
			for (let i = 0; i < localStorage.length; i++) {
				const k = localStorage.key(i);
				if (k.startsWith("viewrab-")) out[k] = localStorage.getItem(k);
			}
		} catch { /* storage refused: nothing kept */ }
		return out;
	};

	const pickFiles = () => new Promise((resolve) => {
		const input = document.createElement("input");
		input.type = "file";
		input.multiple = true;
		input.accept = ".pdf,application/pdf";
		input.addEventListener("change", () => resolve(Array.from(input.files)));
		input.addEventListener("cancel", () => resolve([]));
		input.click();
	});

	const download = ({ name, bytes }) => {
		const a = document.createElement("a");
		a.href = URL.createObjectURL(new Blob([bytes], { type: "application/pdf" }));
		a.download = name;
		document.body.append(a);
		a.click();
		a.remove();
		setTimeout(() => URL.revokeObjectURL(a.href), 60_000);
	};

	// touchGestures turns the touches on the canvas into what the viewer
	// takes from a mouse: a tap clicks; a drag pans, and flung, pans on and
	// slows to a stop; two fingers zoom by their spread and pan by their
	// middle; a long press opens the context menu when the finger lifts, or,
	// dragged on, selects text as the left button does, and then opens it.
	// Pans and zooms are sent once a frame, however often the fingers move.
	const touchGestures = (send) => {
		const slop = 8; // the move, in pixels, below which a touch is a tap
		const holdTime = 500;
		const points = new Map(); // pointer id → its last position
		let g = null; // the gesture going on
		let fling = 0; // the animation frame of a fling, or 0

		// The pan and zoom gathered for the next frame. Pans are sent in whole
		// pixels, and what is left over goes with the next.
		const due = { dx: 0, dy: 0, factor: 1, at: null, frame: 0 };
		const flush = () => {
			due.frame = 0;
			const dx = Math.round(due.dx), dy = Math.round(due.dy);
			due.dx -= dx;
			due.dy -= dy;
			if (due.factor !== 1) send({ type: "pinch", factor: due.factor, ...due.at });
			if (dx || dy) send({ type: "pan", dx, dy, ...due.at });
			due.factor = 1;
		};
		const pan = (dx, dy, at) => {
			Object.assign(due, { dx: due.dx + dx, dy: due.dy + dy, at });
			due.frame ||= requestAnimationFrame(flush);
		};
		const zoom = (factor, at) => {
			Object.assign(due, { factor: due.factor * factor, at });
			due.frame ||= requestAnimationFrame(flush);
		};

		const hover = (p) => send({ type: "pointermove", ...p, ...noMods });
		const button = (type, b, p) => send({ type, button: b, ...p, ...noMods });
		const click = (b, p) => { button("pointerdown", b, p); button("pointerup", b, p); };
		const begin = (kind, p) => ({ kind, start: p, last: p, t: performance.now(), v: { x: 0, y: 0 } });
		const moved = (p) => Math.hypot(p.x - g.start.x, p.y - g.start.y) >= slop;
		const stopFling = () => { cancelAnimationFrame(fling); fling = 0; };
		const spread = () => {
			const [a, b] = [...points.values()];
			return { d: Math.hypot(a.x - b.x, a.y - b.y), mid: { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 } };
		};
		// startFling pans on at velocity v, in pixels a millisecond, slowing
		// as a thrown sheet of paper does.
		const startFling = (v, p) => {
			let last = performance.now();
			const step = (now) => {
				const dt = now - last;
				last = now;
				v = { x: v.x * Math.pow(0.996, dt), y: v.y * Math.pow(0.996, dt) };
				if (Math.hypot(v.x, v.y) < 0.02) { fling = 0; return; }
				pan(-v.x * dt, -v.y * dt, p);
				fling = requestAnimationFrame(step);
			};
			fling = requestAnimationFrame(step);
		};

		const down = (id, p) => {
			stopFling();
			points.set(id, p);
			if (points.size === 1) {
				g = begin("tap", p);
				g.timer = setTimeout(() => {
					if (g?.kind === "tap") g.kind = "hold";
				}, holdTime);
				return;
			}
			if (points.size === 2) {
				clearTimeout(g?.timer);
				if (g?.kind === "select") button("pointerup", 0, g.last);
				g = { kind: "pinch", ...spread() };
			}
		};

		const move = (id, p) => {
			if (!points.has(id) || !g) return;
			points.set(id, p);
			switch (g.kind) {
			case "tap":
				if (!moved(p)) return;
				clearTimeout(g.timer);
				g.kind = "pan";
				// fall through: the move so far pans
			case "pan": {
				const now = performance.now();
				const dt = Math.max(now - g.t, 1);
				const dx = p.x - g.last.x, dy = p.y - g.last.y;
				// The velocity, smoothed over the last few moves.
				g.v = { x: 0.8 * dx / dt + 0.2 * g.v.x, y: 0.8 * dy / dt + 0.2 * g.v.y };
				g.t = now;
				g.last = p;
				pan(-dx, -dy, p);
				return;
			}
			case "hold":
				if (!moved(p)) return;
				g.kind = "select";
				hover(g.start);
				button("pointerdown", 0, g.start);
				// fall through
			case "select":
				g.last = p;
				hover(p);
				return;
			case "pinch": {
				if (points.size < 2) return;
				const now = spread();
				if (g.d > 0 && now.d > 0) zoom(now.d / g.d, now.mid);
				pan(g.mid.x - now.mid.x, g.mid.y - now.mid.y, now.mid);
				Object.assign(g, now);
			}
			}
		};

		const up = (id, p, cancelled = false) => {
			if (!points.delete(id) || !g) return;
			clearTimeout(g.timer);
			if (g.kind === "pinch") {
				// The finger left goes on panning, as a new drag.
				const [rest] = [...points.values()];
				g = rest ? begin("pan", rest) : null;
				return;
			}
			const done = g;
			g = null;
			if (cancelled) {
				if (done.kind === "select") button("pointerup", 0, done.last);
				return;
			}
			switch (done.kind) {
			case "tap":
				hover(done.start);
				click(0, done.start);
				break;
			case "pan":
				// A flick: only a finger still moving as it lifts flings.
				if (performance.now() - done.t < 50) startFling(done.v, p);
				break;
			case "hold":
				click(2, done.start);
				break;
			case "select":
				button("pointerup", 0, p);
				click(2, p);
				break;
			}
		};

		return { down, move, up };
	};

	// The build's version, which make build-web adds to this script's address
	// for the page to add to every other file's, so that a browser never mixes
	// the files of two deployments, and the program's size in bytes, which it
	// gives the script as data-size.
	const script = document.currentScript;
	const version = script ? new URL(script.src).search : "";
	const programSize = Number(script?.dataset.size) || 0;
	// asset returns the address of the build's file at path.
	const asset = (path) => new URL(path + version, location.href).href;

	// loadModule downloads and compiles the program at url as it arrives,
	// calling progress, if given, with the bytes so far. A copy of the
	// download counts them, so that the browser still caches the compiled
	// code, which it does only for a response of its own. A server that does
	// not give the program the application/wasm type streaming needs has it
	// compiled from all of it.
	const loadModule = async (url, progress) => {
		const res = await fetch(url);
		if (!res.ok) throw new Error(res.status + " " + res.statusText);
		const reader = progress && res.clone().body?.getReader();
		if (reader) {
			(async () => {
				let n = 0;
				for (;;) {
					const { done, value } = await reader.read();
					if (done) return;
					progress(n += value.length);
				}
			})().catch(() => {});
		}
		try {
			return await WebAssembly.compileStreaming(res);
		} catch {
			return WebAssembly.compile(await (await fetch(url)).arrayBuffer());
		}
	};

	// start runs the program at wasm in a worker, on the canvas with the id
	// viewrab, whose parent sets the window's size.
	const start = (wasm, worker = "worker.js") => {
		const canvas = document.getElementById("viewrab");
		const box = canvas.parentElement;
		if (!canvas.transferControlToOffscreen || !globalThis.Worker) {
			status("Viewrab needs a browser with OffscreenCanvas and web workers.", true);
			return;
		}
		// The download starts at once, while the worker's scripts load.
		let loading = true;
		let shown = "";
		const mb = (n) => (n / 1e6).toFixed(1);
		status("Loading Viewrab…");
		const module = loadModule(asset(wasm), programSize > 0 && ((n) => {
			const text = `Loading Viewrab… ${mb(Math.min(n, programSize))} of ${mb(programSize)} MB`;
			if (loading && text !== shown) status(shown = text);
		}));
		const w = new Worker(asset(worker));
		const send = (msg, transfer = []) => w.postMessage(msg, transfer);
		const reply = (id, p) => Promise.resolve(p).then(
			(value) => send({ type: "reply", id, value }),
			(err) => send({ type: "reply", id, error: String(err) }));

		w.onmessage = ({ data: { op, arg, id } }) => {
			switch (op) {
			case "status": loading = false; status(arg.text, arg.failed); break;
			case "setTitle": document.title = arg; break;
			case "setCursor": canvas.style.cursor = arg; break;
			case "open": window.open(arg, "_blank", "noopener"); break;
			case "download": download(arg); break;
			case "pickFiles": reply(id, pickFiles()); break;
			case "writeClipboard": navigator.clipboard?.writeText(arg).catch(() => {}); break;
			case "readClipboard":
				navigator.clipboard?.readText?.().then((text) => send({ type: "clipboard", text }), () => {});
				break;
			case "store":
				try {
					if (arg.value === "") localStorage.removeItem(arg.key);
					else localStorage.setItem(arg.key, arg.value);
				} catch { /* storage refused: nothing kept */ }
				break;
			}
		};
		w.onerror = (e) => status("Viewrab could not start: " + (e.message || "the worker failed"), true);

		const dark = matchMedia("(prefers-color-scheme: dark)");
		dark.addEventListener("change", (e) => send({ type: "dark", dark: e.matches }));

		const params = {};
		for (const [k, v] of new URLSearchParams(location.search)) {
			(params[k] ??= []).push(k === "file" ? new URL(v, location.href).href : v);
		}
		// The window's size, in CSS pixels, and the device pixels to each,
		// which the worker draws the canvas in, so that it is sharp.
		const size = () => ({ w: box.clientWidth, h: box.clientHeight, scale: devicePixelRatio || 1 });
		const offscreen = canvas.transferControlToOffscreen();
		send({
			type: "init", canvas: offscreen,
			...size(), params, storage: storage(), prefersDark: dark.matches,
		}, [offscreen]);
		// The worker runs the module compiled here.
		module.then((m) => send({ type: "module", module: m }), (err) => send({ type: "module", error: String(err) }));

		// The canvas fills its parent; the worker draws it at the parent's size.
		const resized = () => send({ type: "resize", ...size() });
		new ResizeObserver(resized).observe(box);
		// The density changes without a resize when the window moves to
		// another display.
		const watchDensity = () => matchMedia(`(resolution: ${devicePixelRatio}dppx)`)
			.addEventListener("change", () => { resized(); watchDensity(); }, { once: true });
		watchDensity();

		canvas.tabIndex = 0;
		const on = (name, f, passive = false) => canvas.addEventListener(name, f, { passive });
		const at = (e) => ({ x: e.offsetX, y: e.offsetY });
		const touch = touchGestures(send);
		const isTouch = (e) => e.pointerType === "touch";
		on("pointerdown", (e) => {
			if (e.button > 2) return;
			e.preventDefault(); // no middle-button autoscroll, no text selection
			canvas.focus();
			try { canvas.setPointerCapture(e.pointerId); } catch { /* lifted already */ }
			if (isTouch(e)) touch.down(e.pointerId, at(e));
			else send({ type: "pointerdown", button: e.button, ...at(e), ...mods(e) });
		});
		on("pointerup", (e) => {
			if (isTouch(e)) touch.up(e.pointerId, at(e));
			else send({ type: "pointerup", button: e.button, ...at(e), ...mods(e) });
		});
		on("pointercancel", (e) => isTouch(e) && touch.up(e.pointerId, at(e), true));
		on("pointermove", (e) => {
			if (isTouch(e)) touch.move(e.pointerId, at(e));
			else send({ type: "pointermove", ...at(e), ...mods(e) });
		}, true);
		for (const t of ["pointerenter", "pointerleave"]) {
			on(t, (e) => isTouch(e) || send({ type: t, ...at(e), ...mods(e) }), true);
		}
		on("contextmenu", (e) => e.preventDefault());
		on("wheel", (e) => {
			e.preventDefault(); // nor the browser's zoom on Ctrl+wheel
			send({ type: "wheel", dx: e.deltaX, dy: e.deltaY, mode: e.deltaMode, h: canvas.clientHeight, ...at(e), ...mods(e) });
		});
		for (const t of ["keydown", "keyup"]) {
			on(t, (e) => {
				if (e.isComposing) return;
				if (prevents(e)) e.preventDefault();
				send({ type: t, key: e.key, code: e.code, ...mods(e) });
			});
		}
		on("focus", () => send({ type: "focus" }), true);
		on("blur", () => send({ type: "blur" }), true);
		document.addEventListener("paste", (e) => {
			e.preventDefault();
			send({ type: "paste", text: e.clipboardData.getData("text/plain") });
		});
		for (const t of ["dragenter", "dragover"]) {
			on(t, (e) => {
				e.preventDefault();
				e.dataTransfer.dropEffect = "copy";
				send({ type: t, ...at(e) });
			});
		}
		on("dragleave", (e) => send({ type: "dragleave", ...at(e) }), true);
		on("drop", (e) => {
			e.preventDefault();
			// Files clone to the worker, which reads them itself.
			send({ type: "drop", files: Array.from(e.dataTransfer.files), ...at(e) });
		});
		canvas.focus();
	};

	globalThis.viewrab = { start };
})();
