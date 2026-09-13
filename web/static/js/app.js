"use strict";

const App = (window.App = window.App || {});

App.flash = (kind, text, undo) => {
    if (!text || !["ok", "error", "info"].includes(kind)) return;
    document.querySelectorAll("body > .flash").forEach((el) => el.remove());
    const el = document.createElement("div");
    el.className = "flash flash-" + kind;
    el.setAttribute("role", "status");
    el.textContent = text;
    if (undo) {
        const csrf = document.querySelector('meta[name="csrf-token"]')?.content || "";
        const form = document.createElement("form");
        form.className = "flash-undo";
        form.method = "post";
        form.action = "/undo/" + undo;
        form.dataset.undo = "";
        const token = document.createElement("input");
        token.type = "hidden";
        token.name = "csrf_token";
        token.value = csrf;
        const button = document.createElement("button");
        button.type = "submit";
        button.className = "btn btn-small";
        button.textContent = "Undo";
        form.append(token, button);
        el.append(form);
    }
    const header = document.querySelector("body > .site-header");
    if (header) header.after(el);
    else document.body.prepend(el);
};

App.readFlash = (res) => {
    const v = res.headers.get("X-Flash");
    if (!v) return null;
    const parts = v.split(":");
    if (parts.length < 3) return null;
    try {
        return { kind: parts[0], undo: parts[1], text: decodeURIComponent(parts.slice(2).join(":")) };
    } catch (_) {
        return null;
    }
};

App.showFlash = (res) => {
    const f = App.readFlash(res);
    if (f) App.flash(f.kind, f.text, f.undo);
};

try {
    const pending = JSON.parse(sessionStorage.getItem("flash") || "null");
    sessionStorage.removeItem("flash");
    if (pending) App.flash(pending.kind, pending.text, pending.undo);
} catch (_) {
    sessionStorage.removeItem("flash");
}

document.addEventListener("keydown", (e) => {
    if (!(e.ctrlKey || e.metaKey) || e.key.toLowerCase() !== "z" || e.shiftKey || e.defaultPrevented) return;
    if (e.target instanceof Element && e.target.closest("input, textarea, select, [contenteditable]")) return;
    const form = document.querySelector(".flash form[data-undo]");
    if (!form) return;
    e.preventDefault();
    form.requestSubmit();
});


const initShowWhen = (root) => {
    root.querySelectorAll("form").forEach((form) => {
        const targets = form.querySelectorAll("[data-show-when]");
        if (!targets.length) return;
        const sync = () => {
            targets.forEach((el) => {
                const [name, value] = el.dataset.showWhen.split("=");
                const field = form.elements.namedItem(name);
                let current = "";
                if (field instanceof RadioNodeList) current = field.value;
                else if (field instanceof HTMLInputElement && field.type === "checkbox") current = field.checked ? field.value : "";
                else if (field) current = field.value;
                el.hidden = current !== value;
            });
        };
        form.addEventListener("change", sync);
        sync();
    });
};

const initForms = (root) => {
    root.querySelectorAll("form.event-form").forEach((form) => {
        const allDay = form.elements.namedItem("all_day");
        const repeat = form.elements.namedItem("repeat");
        const sync = () => {
            form.querySelectorAll("[data-timed]").forEach((el) => {
                el.hidden = Boolean(allDay && allDay.checked);
            });
            form.querySelectorAll("[data-custom-rule]").forEach((el) => {
                el.hidden = !repeat || repeat.value !== "custom";
            });
        };
        if (allDay) allDay.addEventListener("change", sync);
        if (repeat) repeat.addEventListener("change", sync);
        sync();

        const startDate = form.elements.namedItem("start_date");
        const endDate = form.elements.namedItem("end_date");
        if (startDate && endDate) {
            startDate.addEventListener("change", () => {
                if (!endDate.value || endDate.value < startDate.value) endDate.value = startDate.value;
            });
        }
    });
    initShowWhen(root);
};

document.addEventListener("submit", (e) => {
    const form = e.target;
    if (form instanceof HTMLFormElement && form.dataset.confirm && !window.confirm(form.dataset.confirm)) {
        e.preventDefault();
        e.stopImmediatePropagation();
    }
}, true);

initForms(document);

document.querySelectorAll("form[data-autosubmit]").forEach((form) => {
    form.querySelectorAll("select, input[type=checkbox]").forEach((el) => {
        el.addEventListener("change", () => form.requestSubmit());
    });
    form.querySelectorAll("[data-noscript]").forEach((el) => {
        el.hidden = true;
    });
});

document.querySelectorAll("input[data-timezone]").forEach((input) => {
    if (input.value) return;
    try {
        input.value = Intl.DateTimeFormat().resolvedOptions().timeZone || "";
    } catch (_) {
        input.value = "";
    }
});

document.querySelectorAll("[data-copy]").forEach((button) => {
    const target = document.getElementById(button.dataset.copy);
    if (!target || !navigator.clipboard) return;
    button.hidden = false;
    button.addEventListener("click", async () => {
        try {
            await navigator.clipboard.writeText(target.value);
            button.textContent = "Copied";
        } catch (_) {
            target.select();
        }
    });
});

(() => {
    if (typeof HTMLDialogElement !== "function") return;
    const csrf = document.querySelector('meta[name="csrf-token"]')?.content || "";
    let dlg = null;

    const close = () => {
        if (dlg && dlg.open) dlg.close();
    };

    const show = (html) => {
        if (!dlg) {
            dlg = document.createElement("dialog");
            dlg.className = "modal";
            dlg.addEventListener("click", (e) => {
                if (e.target === dlg) close();
            });
            dlg.addEventListener("close", () => dlg.replaceChildren());
            dlg.addEventListener("submit", submit);
            document.body.append(dlg);
        }
        const body = document.createElement("div");
        body.className = "modal-body";
        body.innerHTML = html;
        dlg.replaceChildren(body);
        initForms(body);
        if (!dlg.open) dlg.showModal();
        const focus = body.querySelector("[autofocus]");
        if (focus) focus.focus();
    };

    const fail = (status) => {
        const p = document.createElement("p");
        p.className = "form-error";
        p.setAttribute("role", "alert");
        p.textContent = "Request failed with status " + status + ". Reload the page and try again.";
        dlg.querySelector(".modal-body")?.prepend(p);
    };

    const samePage = (url) =>
        url.origin === window.location.origin &&
        url.pathname === window.location.pathname &&
        url.search === window.location.search;

    const handle = async (res, onError) => {
        const loc = res.headers.get("X-Location");
        if (loc) {
            const target = new URL(loc, window.location.href);
            if (samePage(target) && typeof App.refresh === "function") {
                close();
                App.showFlash(res);
                App.refresh();
                return;
            }
            const f = App.readFlash(res);
            if (f) {
                try {
                    sessionStorage.setItem("flash", JSON.stringify(f));
                } catch (_) {
                    f.text = "";
                }
            }
            if (samePage(target)) {
                window.history.replaceState(null, "", target);
                window.location.reload();
                return;
            }
            window.location.assign(target);
            return;
        }
        if (res.redirected) {
            window.location.assign(res.url);
            return;
        }
        const html = (res.headers.get("Content-Type") || "").startsWith("text/html");
        if (html && (res.ok || res.status === 412 || res.status === 422)) {
            show(await res.text());
            return;
        }
        onError(res.status);
    };

    const load = async (url) => {
        try {
            const res = await fetch(url, { headers: { "X-Fragment": "1" }, credentials: "same-origin" });
            await handle(res, () => window.location.assign(url));
        } catch (_) {
            window.location.assign(url);
        }
    };

    async function submit(e) {
        const form = e.target;
        if (!(form instanceof HTMLFormElement) || form.method.toLowerCase() !== "post") return;
        e.preventDefault();
        const data = e.submitter ? new FormData(form, e.submitter) : new FormData(form);
        form.querySelectorAll("button[type=submit]").forEach((b) => {
            b.disabled = true;
        });
        try {
            const res = await fetch(form.action, {
                method: "POST",
                headers: {
                    "X-Fragment": "1",
                    "X-CSRF-Token": csrf,
                    "Content-Type": "application/x-www-form-urlencoded",
                },
                body: new URLSearchParams(data),
                credentials: "same-origin",
            });
            await handle(res, fail);
        } catch (_) {
            fail("network error");
        } finally {
            form.querySelectorAll("button[type=submit]").forEach((b) => {
                b.disabled = false;
            });
        }
    }

    document.addEventListener("click", (e) => {
        if (e.defaultPrevented || e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
        const el = e.target instanceof Element ? e.target : null;
        if (!el || el.closest("[data-toggle]")) return;
        const closer = el.closest("[data-close]");
        if (closer && dlg && dlg.contains(closer)) {
            e.preventDefault();
            close();
            return;
        }
        const link = el.closest("a[data-dialog]");
        if (!link) return;
        e.preventDefault();
        load(link.href);
    });

    App.dialog = load;
})();

document.addEventListener("keydown", (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented) return;
    if (document.querySelector("dialog[open]")) return;
    if (e.target instanceof Element && e.target.closest("input, textarea, select, [contenteditable]")) return;
    if (e.key !== "/") return;
    const box = document.querySelector("[data-search]");
    if (!box) return;
    e.preventDefault();
    box.focus();
});

// Quick-add echo. Client-side only: the server re-parses the text, so the
// preview can never disagree with what actually gets stored.
document.querySelectorAll("form[data-quick]").forEach((quick) => {
    const input = quick.querySelector("input[name=title]");
    const hint = quick.querySelector("[data-quick-hint]");
    if (!input || !hint) return;
    const bits = [
        [/\b(today|tonight|tomorrow|tmr|yesterday)\b/i, (m) => m[1].toLowerCase()],
        [/\bin\s+\d{1,3}\s*(?:d|days?|w|weeks?)\b/i, (m) => m[0].toLowerCase()],
        [/\b(?:next\s+|this\s+)?(?:mon|tue|wed|thu|fri|sat|sun)[a-z]*\b/i, (m) => m[0].toLowerCase()],
        [/\b\d{4}-\d{2}-\d{2}\b/, (m) => m[0]],
        [/\b(?:at\s+)?([01]?\d|2[0-3])[:.][0-5]\d\s*(?:am|pm)?\b/i, (m) => m[0].trim()],
        [/\bfor\s+\d{1,3}\s*(?:m|min|mins|minutes|h|hr|hrs|hours)\b/i, (m) => m[0].toLowerCase()],
        [/(?:^|\s)#[\p{L}\p{N}_-]+/u, (m) => m[0].trim()],
        [/(?:^|\s)(!{1,3})(?:\s|$)/, (m) => "priority " + m[1].length],
    ];
    const sync = () => {
        const found = [];
        for (const [re, label] of bits) {
            const m = input.value.match(re);
            if (m) found.push(label(m));
        }
        hint.textContent = found.length ? "Detected: " + found.join(" · ") : "";
        hint.hidden = found.length === 0;
    };
    input.addEventListener("input", sync);
    sync();
});

document.addEventListener("keydown", (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented) return;
    if (e.key !== "?") return;
    if (e.target instanceof Element && e.target.closest("input, textarea, select, [contenteditable]")) return;
    if (document.querySelector("dialog[open]")) return;
    const help = document.getElementById("help");
    if (!help || typeof help.showModal !== "function") return;
    e.preventDefault();
    help.showModal();
});
