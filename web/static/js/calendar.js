"use strict";

(() => {
    const csrf = document.querySelector('meta[name="csrf-token"]')?.content || "";

    const viewerTZ = document.querySelector("[data-viewer-tz]");
    if (viewerTZ) {
        let tz = "";
        try {
            tz = Intl.DateTimeFormat().resolvedOptions().timeZone || "";
        } catch (_) {
            tz = "";
        }
        const here = new URL(window.location.href);
        if (tz && here.searchParams.get("tz") !== tz) {
            here.searchParams.set("tz", tz);
            window.location.replace(here);
            return;
        }
    }

    document.addEventListener("click", async (e) => {
        const tick = e.target instanceof Element ? e.target.closest("[data-toggle]") : null;
        if (!tick) return;
        e.preventDefault();
        e.stopPropagation();
        try {
            const res = await fetch(tick.dataset.toggle, {
                method: "POST",
                headers: { "X-Fragment": "1", "X-CSRF-Token": csrf },
                credentials: "same-origin",
            });
            window.App.showFlash(res);
            if (res.ok) await refresh();
            else window.location.reload();
        } catch (_) {
            window.location.reload();
        }
    });

    // Narrow viewports drop the grid's own scroller and let the page scroll, so
    // the offset has to be applied to whichever of the two actually scrolls.
    const scrollerOf = (grid) => {
        const el = grid.querySelector(".grid-scroll");
        return el && el.scrollHeight > el.clientHeight ? el : null;
    };

    const gridScrollTop = (grid) => {
        const el = scrollerOf(grid);
        return el ? el.scrollTop : window.scrollY;
    };

    const placeGrid = (grid, top) => {
        const scroller = scrollerOf(grid);
        if (typeof top === "number") {
            if (scroller) scroller.scrollTop = top;
            else window.scrollTo(0, top);
            return;
        }
        const now = grid.querySelector("[data-now]");
        const target = now || grid.querySelectorAll(".hour")[Number(grid.dataset.scroll) || 0];
        if (!target) return;
        if (scroller) {
            scroller.scrollTop = Math.max(0, target.offsetTop - scroller.clientHeight / 3);
            return;
        }
        const y = target.getBoundingClientRect().top + window.scrollY;
        window.scrollTo(0, Math.max(0, y - window.innerHeight / 3));
    };

    const moveNow = () => {
        const grid = document.querySelector(".grid");
        const now = grid ? grid.querySelector("[data-now]") : null;
        if (!now) return;
        const parts = new Intl.DateTimeFormat("en-CA", {
            timeZone: grid.dataset.tz,
            year: "numeric",
            month: "2-digit",
            day: "2-digit",
            hour: "2-digit",
            minute: "2-digit",
            hourCycle: "h23",
        }).formatToParts(new Date());
        const p = Object.fromEntries(parts.map((x) => [x.type, x.value]));
        if (`${p.year}-${p.month}-${p.day}` !== grid.dataset.today) {
            window.location.reload();
            return;
        }
        now.className = "now gr-" + (Math.floor((Number(p.hour) * 60 + Number(p.minute)) / 5) + 1);
    };

    document.querySelectorAll(".grid").forEach((g) => placeGrid(g));
    window.setInterval(moveNow, 30000);
    document.addEventListener("visibilitychange", () => {
        if (!document.hidden) moveNow();
    });

    // Swaps the live region for a freshly rendered one instead of reloading the
    // page. Shared by the drag drop and the websocket, so both keep the grid's
    // scroll position. Falls back to a reload when there is no live region.
    let live = document.querySelector("[data-live]");
    let busy = false;
    let again = false;

    const refresh = async () => {
        if (!live) {
            window.location.reload();
            return;
        }
        if (busy) {
            again = true;
            return;
        }
        busy = true;
        try {
            const res = await fetch(window.location.href, {
                headers: { "X-Fragment": "1" },
                credentials: "same-origin",
                cache: "no-store",
            });
            if (res.redirected || res.status === 404) {
                window.location.reload();
                return;
            }
            if (!res.ok) return;
            const tpl = document.createElement("template");
            tpl.innerHTML = await res.text();
            const next = tpl.content.querySelector("[data-live]");
            if (!next) return;
            const prev = live.querySelector(".grid");
            const top = prev ? gridScrollTop(prev) : undefined;
            live.replaceWith(next);
            live = next;
            const grid = next.querySelector(".grid");
            if (grid) placeGrid(grid, top);
        } catch (_) {
            return;
        } finally {
            busy = false;
            if (again) {
                again = false;
                refresh();
            }
        }
    };

    window.App.refresh = refresh;

    // Drag a timed block to another day and time. The column is 288 five-minute
    // rows, so one row is the snap unit. Touch is left out on purpose: starting
    // a drag from a block would otherwise swallow the scroll gesture.
    const SLOTS = 288;
    const SNAP = 3; // 15 minutes; hold Alt for the full 5-minute resolution
    let drag = null;
    let dragEnded = 0;

    // The hour gutter is rendered with the account's clock format, so it says
    // which one to mirror while dragging.
    const clock12 = [...document.querySelectorAll(".grid-hours .hour")].some((h) => /[AP]M/i.test(h.textContent));

    const clock = (mins) => {
        const m = ((mins % 1440) + 1440) % 1440;
        const h = Math.floor(m / 60);
        const mm = String(m % 60).padStart(2, "0");
        if (!clock12) return String(h).padStart(2, "0") + ":" + mm;
        return (h % 12 === 0 ? 12 : h % 12) + ":" + mm + " " + (h < 12 ? "AM" : "PM");
    };

    // Snap the absolute row, not the delta, so an event that starts at 10:05
    // lands on a round time rather than staying five minutes off it.
    const snapRow = (row, step) => Math.round((row - 1) / step) * step + 1;

    const classNum = (el, prefix) => {
        for (const c of el.classList) {
            if (c.startsWith(prefix)) return Number(c.slice(prefix.length));
        }
        return 0;
    };

    const setClass = (el, prefix, n) => {
        for (const c of [...el.classList]) {
            if (c.startsWith(prefix)) el.classList.remove(c);
        }
        el.classList.add(prefix + n);
    };

    const endDrag = () => {
        if (!drag) return null;
        const d = drag;
        drag = null;
        d.block.classList.remove("dragging", "resizing");
        d.grid.classList.remove("dragging");
        if (d.moved) dragEnded = Date.now();
        return d;
    };

    document.addEventListener("pointerdown", (e) => {
        if (e.button !== 0 || e.pointerType === "touch") return;
        const el = e.target instanceof Element ? e.target : null;
        if (!el || el.closest("[data-toggle]")) return;
        const block = el.closest(".block[data-move]");
        const col = block && block.closest(".grid-col");
        const grid = block && block.closest(".grid");
        if (!block || !col || !grid) return;
        const grip = el.closest(".grip");
        const mode = grip ? grip.dataset.grip : "move";
        const cols = [...grid.querySelectorAll(".grid-col")];
        const rect = col.getBoundingClientRect();
        const row = classNum(block, "gr-");
        drag = {
            block, grid, cols, rect,
            pointer: e.pointerId,
            col0: cols.indexOf(col),
            row0: row,
            row, col: cols.indexOf(col),
            span0: classNum(block, "gs-") || 1,
            span: classNum(block, "gs-") || 1,
            x0: e.clientX, y0: e.clientY,
            moved: false,
            mode,
            timeEl: block.querySelector(".block-time"),
        };
        try {
            block.setPointerCapture(e.pointerId);
        } catch (_) {
            // capture is an optimisation; the document listeners still fire
        }
    });

    document.addEventListener("pointermove", (e) => {
        if (!drag || e.pointerId !== drag.pointer) return;
        const dx = e.clientX - drag.x0;
        const dy = e.clientY - drag.y0;
        if (!drag.moved) {
            if (Math.abs(dx) < 4 && Math.abs(dy) < 4) return;
            drag.moved = true;
            drag.block.classList.add(drag.mode === "move" ? "dragging" : "resizing");
            drag.grid.classList.add("dragging");
        }
        const slot = drag.rect.height / SLOTS;
        const step = e.altKey ? 1 : SNAP;
        const shift = dy / slot;
        const end0 = drag.row0 + drag.span0;
        let row = drag.row, col = drag.col, span = drag.span;
        if (drag.mode === "start") {
            // top edge: the end stays where it is, so the span absorbs the move
            row = Math.min(Math.max(snapRow(drag.row0 + shift, step), 1), end0 - 1);
            span = end0 - row;
        } else if (drag.mode === "end") {
            const bottom = Math.min(Math.max(snapRow(end0 + shift, step), drag.row0 + 1), SLOTS + 1);
            span = bottom - drag.row0;
        } else {
            row = Math.min(Math.max(snapRow(drag.row0 + shift, step), 1), SLOTS - drag.span0 + 1);
            col = Math.min(Math.max(drag.col0 + Math.round(dx / drag.rect.width), 0), drag.cols.length - 1);
        }
        if (row === drag.row && col === drag.col && span === drag.span) return;
        drag.row = row;
        drag.col = col;
        drag.span = span;
        setClass(drag.block, "gr-", row);
        setClass(drag.block, "gs-", span);
        if (drag.timeEl) {
            drag.timeEl.textContent = clock((row - 1) * 5) + " – " + clock((row + span - 1) * 5);
        }
        if (drag.block.parentElement !== drag.cols[col]) drag.cols[col].append(drag.block);
    });

    document.addEventListener("pointerup", async (e) => {
        if (!drag || e.pointerId !== drag.pointer) return;
        const d = endDrag();
        if (!d.moved || (d.row === d.row0 && d.col === d.col0 && d.span === d.span0)) return;
        // The grid runs past midnight, so an end at row 289 is the next day 00:00.
        const stamp = (row) => {
            const mins = (row - 1) * 5;
            const day = new Date(d.cols[d.col].dataset.date + "T00:00:00Z");
            day.setUTCDate(day.getUTCDate() + Math.floor(mins / 1440));
            const rest = ((mins % 1440) + 1440) % 1440;
            return day.toISOString().slice(0, 10) + "T" +
                String(Math.floor(rest / 60)).padStart(2, "0") + ":" + String(rest % 60).padStart(2, "0");
        };
        const body = new URLSearchParams();
        if (d.mode !== "end") body.set("start", stamp(d.row));
        // a top-edge resize must pin the end, or the server would keep the duration
        if (d.mode !== "move") body.set("end", stamp(d.row + d.span));
        if (d.block.dataset.instance) body.set("instance", d.block.dataset.instance);
        try {
            const res = await fetch(d.block.dataset.move, {
                method: "POST",
                headers: { "X-Fragment": "1", "X-CSRF-Token": csrf, "Content-Type": "application/x-www-form-urlencoded" },
                body,
                credentials: "same-origin",
            });
            window.App.showFlash(res);
            await refresh();
        } catch (_) {
            window.location.reload();
        }
    });

    // Dragging empty space drafts an event: a provisional block follows the
    // pointer, and letting go opens the dialog pre-filled with the span drawn.
    // A plain click with no movement falls through to the slot's own link.
    const MIN_SPAN = 2;
    let draft = null;

    const endDraft = () => {
        const d = draft;
        draft = null;
        if (!d) return null;
        d.el.remove();
        d.grid.classList.remove("dragging");
        if (d.moved) dragEnded = Date.now();
        return d;
    };

    const dayOf = (col) => col.dataset.date;

    const hhmm = (row) => {
        const mins = ((row - 1) * 5) % 1440;
        return String(Math.floor(mins / 60)).padStart(2, "0") + ":" + String(mins % 60).padStart(2, "0");
    };

    document.addEventListener("pointerdown", (e) => {
        if (e.button !== 0 || e.pointerType === "touch" || drag) return;
        const el = e.target instanceof Element ? e.target : null;
        if (!el || el.closest(".block") || el.closest(".bar") || el.closest("[data-toggle]")) return;

        const slot = el.closest(".slot");
        const col = slot && slot.closest(".grid-col");
        const grid = col && col.closest(".grid");
        if (slot && col && grid) {
            const rect = col.getBoundingClientRect();
            const row = snapRow(classNum(slot, "gr-") || 1, SNAP);
            const el2 = document.createElement("div");
            el2.className = "block provisional gr-" + row + " gs-" + MIN_SPAN;
            col.append(el2);
            draft = {
                kind: "timed", el: el2, grid, col, rect,
                pointer: e.pointerId, row0: row, row, span: MIN_SPAN, moved: false,
            };
            return;
        }

        const lanes = el.closest(".allday-lanes");
        const allDayGrid = lanes && lanes.closest(".grid");
        if (lanes && allDayGrid) {
            const cols = [...allDayGrid.querySelectorAll(".grid-col")];
            if (!cols.length) return;
            const rect = lanes.getBoundingClientRect();
            const width = rect.width / cols.length;
            const index = Math.min(Math.max(Math.floor((e.clientX - rect.left) / width), 0), cols.length - 1);
            const el2 = document.createElement("div");
            el2.className = "bar provisional gcol-" + (index + 1) + " gspan-1 grow-1";
            lanes.append(el2);
            draft = {
                kind: "allday", el: el2, grid: allDayGrid, cols, rect, width,
                pointer: e.pointerId, col0: index, col: index, span: 1, moved: false,
            };
        }
    });

    document.addEventListener("pointermove", (e) => {
        if (!draft || e.pointerId !== draft.pointer) return;
        if (draft.kind === "timed") {
            const slotHeight = draft.rect.height / SLOTS;
            const shift = (e.clientY - draft.rect.top) / slotHeight;
            const end = Math.min(Math.max(snapRow(shift, e.altKey ? 1 : SNAP), draft.row0 + MIN_SPAN), SLOTS + 1);
            const span = end - draft.row0;
            if (span === draft.span && draft.moved) return;
            draft.moved = true;
            draft.span = span;
            draft.grid.classList.add("dragging");
            setClass(draft.el, "gs-", span);
            return;
        }
        const index = Math.min(Math.max(Math.floor((e.clientX - draft.rect.left) / draft.width), 0), draft.cols.length - 1);
        const from = Math.min(index, draft.col0);
        const span = Math.abs(index - draft.col0) + 1;
        if (from === draft.col && span === draft.span && draft.moved) return;
        draft.moved = true;
        draft.col = from;
        draft.span = span;
        draft.grid.classList.add("dragging");
        setClass(draft.el, "gcol-", from + 1);
        setClass(draft.el, "gspan-", span);
    });

    document.addEventListener("pointerup", (e) => {
        if (!draft || e.pointerId !== draft.pointer) return;
        const d = endDraft();
        if (!d.moved) return;
        e.preventDefault();
        const p = new URLSearchParams();
        if (d.kind === "timed") {
            p.set("date", dayOf(d.col));
            p.set("time", hhmm(d.row0));
            p.set("end", hhmm(d.row0 + d.span));
        } else {
            p.set("date", dayOf(d.cols[d.col]));
            p.set("all_day", "1");
            p.set("end_date", dayOf(d.cols[Math.min(d.col + d.span - 1, d.cols.length - 1)]));
        }
        if (typeof window.App.dialog === "function") {
            window.App.dialog("/events/new?" + p.toString());
        } else {
            window.location.assign("/events/new?" + p.toString());
        }
    });

    document.addEventListener("pointercancel", endDraft);

    // A .block is an <a href>, which browsers drag natively: a real mousedown
    // plus move starts a link drag and cancels the pointer stream. Synthetic
    // events never do this, so it only shows up with a real mouse.
    document.addEventListener("dragstart", (e) => {
        const el = e.target instanceof Element ? e.target : null;
        if (el && (el.closest(".block[data-move]") || el.closest(".slot"))) e.preventDefault();
    });

    document.addEventListener("pointercancel", endDrag);

    // A drag ends with a click on the block, which would otherwise open the
    // dialog. Capture phase so this runs before the dialog handler.
    document.addEventListener("click", (e) => {
        if (Date.now() - dragEnded > 300) return;
        dragEnded = 0;
        e.preventDefault();
        e.stopPropagation();
    }, true);

    const keys = {};
    document.querySelectorAll("a[data-key]").forEach((a) => {
        keys[a.dataset.key] = a;
    });
    document.addEventListener("keydown", (e) => {
        if (e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented) return;
        if (document.querySelector("dialog[open]")) return;
        if (e.target instanceof Element && e.target.closest("input, textarea, select, [contenteditable]")) return;
        const link = keys[e.key];
        if (!link) return;
        e.preventDefault();
        link.click();
    });

    const shareForm = document.querySelector("form[data-share]");
    if (shareForm) {
        shareForm.addEventListener("submit", async (e) => {
            e.preventDefault();
            const button = shareForm.querySelector("button[type=submit]");
            button.disabled = true;
            try {
                const res = await fetch(shareForm.action, {
                    method: "POST",
                    headers: { "X-Fragment": "1", "X-CSRF-Token": csrf, "Content-Type": "application/x-www-form-urlencoded" },
                    body: new URLSearchParams(new FormData(shareForm)),
                    credentials: "same-origin",
                });
                const link = res.headers.get("X-Share-URL");
                if (!res.ok && res.status !== 204) {
                    window.App.showFlash(res);
                    return;
                }
                if (link && navigator.clipboard) {
                    await navigator.clipboard.writeText(link);
                    window.App.flash("ok", "Share link created and copied.");
                } else if (link) {
                    window.prompt("Share link", link);
                }
                const edit = res.headers.get("X-Share-Edit");
                if (edit && typeof window.App.dialog === "function") {
                    await window.App.dialog(edit);
                }
            } catch (_) {
                window.App.flash("error", "Could not create the share link.");
            } finally {
                button.disabled = false;
            }
        });
    }

    let socket = null;
    let retry = 1000;
    let timer = 0;

    if (!live || !live.dataset.live || typeof WebSocket !== "function") return;

    const notify = (msg) => {
        const text = msg.body ? msg.title + " — " + msg.body : msg.title;
        if (typeof Notification === "function" && Notification.permission === "granted") {
            try {
                const n = new Notification(msg.title, { body: msg.body || "", tag: msg.tag || "calendar" });
                n.addEventListener("click", () => {
                    window.focus();
                    if (msg.url) window.location.assign(msg.url);
                });
                return;
            } catch (_) {
                // fall through to the banner
            }
        }
        window.App.flash("info", text);
    };


    const connect = () => {
        const u = new URL(live.dataset.live, window.location.href);
        u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
        u.searchParams.set("since", live.dataset.seq || "0");
        const ws = new WebSocket(u);
        socket = ws;
        let opened = false;
        ws.addEventListener("open", () => {
            opened = true;
            retry = 1000;
        });
        ws.addEventListener("message", (e) => {
            let msg;
            try {
                msg = JSON.parse(e.data);
            } catch (_) {
                return;
            }
            if (msg.type === "reload") {
                window.location.reload();
            } else if (msg.type === "refresh") {
                window.clearTimeout(timer);
                timer = window.setTimeout(refresh, 200);
            } else if (msg.type === "notice") {
                notify(msg);
            }
        });
        ws.addEventListener("close", () => {
            if (socket === ws) socket = null;
            if (!opened) refresh();
            window.setTimeout(() => {
                if (!socket && !document.hidden) connect();
            }, retry);
            retry = Math.min(retry * 2, 30000);
        });
    };

    connect();
    document.addEventListener("visibilitychange", () => {
        if (!document.hidden && !socket) connect();
    });
})();