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

	// start runs the program at wasm in a worker, on the canvas with the id
	// viewrab, whose parent sets the window's size.
	const start = (wasm, worker = "worker.js") => {
		const canvas = document.getElementById("viewrab");
		const box = canvas.parentElement;
		if (!canvas.transferControlToOffscreen || !globalThis.Worker) {
			status("Viewrab needs a browser with OffscreenCanvas and web workers.", true);
			return;
		}
		status("Loading Viewrab…");
		const w = new Worker(worker);
		const send = (msg, transfer = []) => w.postMessage(msg, transfer);
		const reply = (id, p) => Promise.resolve(p).then(
			(value) => send({ type: "reply", id, value }),
			(err) => send({ type: "reply", id, error: String(err) }));

		w.onmessage = ({ data: { op, arg, id } }) => {
			switch (op) {
			case "status": status(arg.text, arg.failed); break;
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
		const offscreen = canvas.transferControlToOffscreen();
		send({
			type: "init", canvas: offscreen, wasm: new URL(wasm, location.href).href,
			w: box.clientWidth, h: box.clientHeight,
			params, storage: storage(), prefersDark: dark.matches,
		}, [offscreen]);

		// The canvas fills its parent; the worker draws it at the parent's size.
		new ResizeObserver(() => send({ type: "resize", w: box.clientWidth, h: box.clientHeight })).observe(box);

		canvas.tabIndex = 0;
		const on = (name, f, passive = false) => canvas.addEventListener(name, f, { passive });
		const at = (e) => ({ x: e.offsetX, y: e.offsetY });
		on("pointerdown", (e) => {
			if (e.button > 2) return;
			e.preventDefault(); // no middle-button autoscroll, no text selection
			canvas.focus();
			try { canvas.setPointerCapture(e.pointerId); } catch { /* lifted already */ }
			send({ type: "pointerdown", button: e.button, ...at(e), ...mods(e) });
		});
		on("pointerup", (e) => send({ type: "pointerup", button: e.button, ...at(e), ...mods(e) }));
		for (const t of ["pointermove", "pointerenter", "pointerleave"]) {
			on(t, (e) => send({ type: t, ...at(e), ...mods(e) }), true);
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
