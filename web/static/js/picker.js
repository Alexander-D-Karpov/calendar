// Upgrades native date and time inputs into typeable pickers. The native
// control is left in the DOM and keeps its name and value format, so a failure
// here degrades to the browser's own picker instead of breaking the form.
(() => {
    const TIME_STEP = 15;
    const DOW = ["Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"];
    const MONTHS = [
        "January", "February", "March", "April", "May", "June",
        "July", "August", "September", "October", "November", "December",
    ];

    let openPop = null;

    const pad = (n) => String(n).padStart(2, "0");
    const hhmm = (mins) => `${pad(Math.floor(mins / 60) % 24)}:${pad(mins % 60)}`;
    const iso = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;

    function parseISO(s) {
        const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec((s || "").trim());
        if (!m) return null;
        const d = new Date(+m[1], +m[2] - 1, +m[3]);
        return Number.isNaN(d.getTime()) ? null : d;
    }

    // Accepts what people actually type: 9, 930, 9:5, 9 pm, 21.30.
    function parseTime(raw) {
        let s = (raw || "").trim().toLowerCase();
        if (!s) return null;
        let pm = false, am = false;
        if (s.endsWith("pm")) { pm = true; s = s.slice(0, -2).trim(); }
        else if (s.endsWith("am")) { am = true; s = s.slice(0, -2).trim(); }
        s = s.replace(/[.\s]/g, ":");
        let h, min;
        const colon = /^(\d{1,2}):(\d{1,2})$/.exec(s);
        if (colon) {
            h = +colon[1];
            min = +colon[2];
        } else if (/^\d{1,2}$/.test(s)) {
            h = +s;
            min = 0;
        } else if (/^\d{3,4}$/.test(s)) {
            h = +s.slice(0, s.length - 2);
            min = +s.slice(-2);
        } else {
            return null;
        }
        if (pm && h < 12) h += 12;
        if (am && h === 12) h = 0;
        if (h > 23 || min > 59) return null;
        return h * 60 + min;
    }

    function closePop() {
        if (!openPop) return;
        openPop.el.removeAttribute("data-open");
        openPop.el.removeAttribute("data-above");
        openPop.input.setAttribute("aria-expanded", "false");
        openPop = null;
    }

    function showPop(pop, input, onKey) {
        if (openPop && openPop.el !== pop) closePop();
        pop.setAttribute("data-open", "");
        input.setAttribute("aria-expanded", "true");
        openPop = { el: pop, input, onKey };
        // Flip above the field when the popover would run past the viewport.
        if (window.matchMedia("(min-width: 30rem)").matches) {
            const r = pop.getBoundingClientRect();
            if (r.bottom > window.innerHeight && input.getBoundingClientRect().top > r.height) {
                pop.setAttribute("data-above", "");
            }
        }
    }

    function wrap(input) {
        const box = document.createElement("span");
        box.className = "pick";
        input.parentNode.insertBefore(box, input);
        box.appendChild(input);
        input.type = "text";
        input.autocomplete = "off";
        input.spellcheck = false;
        input.setAttribute("role", "combobox");
        input.setAttribute("aria-expanded", "false");
        input.setAttribute("aria-autocomplete", "list");
        return box;
    }

    /* Time ---------------------------------------------------------------- */

    function enhanceTime(input) {
        const box = wrap(input);
        input.setAttribute("inputmode", "numeric");
        input.placeholder = input.placeholder || "--:--";
        // A text input defaults to size=20. Without this the upgraded field is
        // twice the width of the native time control it replaced and pushes the
        // whole row off a narrow screen.
        input.size = 5;

        const pop = document.createElement("div");
        pop.className = "pick-pop";
        const list = document.createElement("ul");
        list.className = "pick-list";
        list.setAttribute("role", "listbox");
        pop.appendChild(list);
        box.appendChild(pop);

        // The end field shows how long the event runs, which is the number
        // people actually reason about when picking an end time.
        const form = input.closest("form");
        const isEnd = input.name === "end_time";
        const startInput = isEnd && form ? form.querySelector('input[name="start_time"]') : null;

        let active = -1;
        // Only narrow the list while the user is typing. Opening a field that
        // already holds a value must still offer every other time, or editing
        // an event means clearing the field before you can pick a new one.
        let filtering = false;

        function options() {
            const base = parseTime(startInput && startInput.value) ?? null;
            const out = [];
            for (let m = 0; m < 24 * 60; m += TIME_STEP) {
                let note = "";
                if (base !== null) {
                    const diff = m - base;
                    if (diff <= 0) continue;
                    const h = Math.floor(diff / 60);
                    const mm = diff % 60;
                    note = h ? (mm ? `${h} h ${mm} m` : `${h} h`) : `${mm} m`;
                }
                out.push({ value: hhmm(m), note });
            }
            return out;
        }

        function render() {
            const typed = filtering ? input.value.trim() : "";
            const all = options();
            const shown = typed ? all.filter((o) => o.value.startsWith(typed)) : all;
            list.textContent = "";
            if (!shown.length) {
                const p = document.createElement("li");
                p.className = "pick-empty";
                p.textContent = "No matching time";
                list.appendChild(p);
                active = -1;
                return shown;
            }
            shown.forEach((o, i) => {
                const li = document.createElement("li");
                li.className = "pick-opt";
                li.setAttribute("role", "option");
                li.setAttribute("aria-selected", o.value === input.value ? "true" : "false");
                const v = document.createElement("span");
                v.textContent = o.value;
                li.appendChild(v);
                if (o.note) {
                    const n = document.createElement("span");
                    n.className = "pick-opt-note";
                    n.textContent = o.note;
                    li.appendChild(n);
                }
                li.addEventListener("mousedown", (e) => {
                    e.preventDefault();
                    commit(o.value);
                });
                if (i === active) li.classList.add("is-active");
                list.appendChild(li);
            });
            if (active < 0) {
                const sel = shown.findIndex((o) => o.value === input.value);
                if (sel >= 0) setActive(sel, false);
            }
            return shown;
        }

        function setActive(i, scroll = true) {
            const items = list.querySelectorAll(".pick-opt");
            if (!items.length) return;
            active = Math.max(0, Math.min(i, items.length - 1));
            items.forEach((el, n) => el.classList.toggle("is-active", n === active));
            if (scroll) items[active].scrollIntoView({ block: "nearest" });
        }

        function commit(value) {
            input.value = value;
            input.dispatchEvent(new Event("change", { bubbles: true }));
            closePop();
        }

        function open() {
            active = -1;
            filtering = false;
            render();
            showPop(pop, input, onKey);
            const cur = list.querySelector('[aria-selected="true"]');
            if (cur) cur.scrollIntoView({ block: "center" });
        }

        function onKey(e) {
            if (e.key === "ArrowDown") { e.preventDefault(); setActive(active + 1); }
            else if (e.key === "ArrowUp") { e.preventDefault(); setActive(active - 1); }
            else if (e.key === "Enter") {
                const items = list.querySelectorAll(".pick-opt");
                if (active >= 0 && items[active]) {
                    e.preventDefault();
                    commit(items[active].firstChild.textContent);
                }
            }
        }

        input.addEventListener("focus", open);
        input.addEventListener("mousedown", () => { if (!openPop) open(); });
        input.addEventListener("input", () => { active = -1; filtering = true; render(); });
        input.addEventListener("blur", () => {
            const mins = parseTime(input.value);
            if (mins !== null) input.value = hhmm(mins);
            else if (input.value.trim()) input.value = "";
        });
        if (startInput) startInput.addEventListener("change", () => { if (openPop) render(); });
    }

    /* Date ---------------------------------------------------------------- */

    function enhanceDate(input) {
        const box = wrap(input);
        input.placeholder = input.placeholder || "yyyy-mm-dd";
        input.size = 10;

        const pop = document.createElement("div");
        pop.className = "pick-pop";
        const cal = document.createElement("div");
        cal.className = "pick-cal";
        pop.appendChild(cal);
        box.appendChild(pop);

        let cursor = null;
        // Returning focus to the field would re-fire its focus handler and
        // pop the calendar straight back open, because a real mouse click
        // leaves focus on the day button rather than the input.
        let refocusing = false;

        function commit(value) {
            input.value = value;
            input.dispatchEvent(new Event("change", { bubbles: true }));
            closePop();
            refocusing = true;
            input.focus();
            refocusing = false;
        }

        function draw() {
            const selected = parseISO(input.value);
            const today = new Date();
            const base = cursor || selected || today;
            cursor = new Date(base.getFullYear(), base.getMonth(), 1);

            cal.textContent = "";

            const head = document.createElement("div");
            head.className = "pick-cal-head";
            const mk = (label, delta) => {
                const b = document.createElement("button");
                b.type = "button";
                b.className = "pick-cal-nav";
                b.textContent = label;
                b.setAttribute("aria-label", delta < 0 ? "Previous month" : "Next month");
                b.addEventListener("click", () => {
                    cursor = new Date(cursor.getFullYear(), cursor.getMonth() + delta, 1);
                    draw();
                });
                return b;
            };
            const title = document.createElement("span");
            title.className = "pick-cal-title";
            title.textContent = `${MONTHS[cursor.getMonth()]} ${cursor.getFullYear()}`;
            head.append(mk("‹", -1), title, mk("›", 1));
            cal.appendChild(head);

            const grid = document.createElement("div");
            grid.className = "pick-grid";
            DOW.forEach((d) => {
                const c = document.createElement("div");
                c.className = "pick-dow";
                c.textContent = d;
                grid.appendChild(c);
            });

            // Monday-first: getDay() is Sunday-based, so Sunday maps to 6.
            const first = new Date(cursor.getFullYear(), cursor.getMonth(), 1);
            const lead = (first.getDay() + 6) % 7;
            const start = new Date(first);
            start.setDate(1 - lead);
            for (let i = 0; i < 42; i++) {
                const d = new Date(start.getFullYear(), start.getMonth(), start.getDate() + i);
                const b = document.createElement("button");
                b.type = "button";
                b.className = "pick-day";
                b.textContent = String(d.getDate());
                if (d.getMonth() !== cursor.getMonth()) b.setAttribute("data-other", "");
                if (iso(d) === iso(today)) b.setAttribute("data-today", "");
                b.setAttribute("aria-selected", selected && iso(d) === iso(selected) ? "true" : "false");
                b.addEventListener("click", () => commit(iso(d)));
                grid.appendChild(b);
            }
            cal.appendChild(grid);

            const foot = document.createElement("div");
            foot.className = "pick-cal-foot";
            const todayBtn = document.createElement("button");
            todayBtn.type = "button";
            todayBtn.textContent = "Today";
            todayBtn.addEventListener("click", () => commit(iso(new Date())));
            foot.appendChild(todayBtn);
            if (!input.required) {
                const clear = document.createElement("button");
                clear.type = "button";
                clear.textContent = "Clear";
                clear.addEventListener("click", () => commit(""));
                foot.appendChild(clear);
            }
            cal.appendChild(foot);
        }

        function open() {
            if (refocusing) return;
            cursor = null;
            draw();
            showPop(pop, input, onKey);
        }

        function onKey(e) {
            if (e.key !== "ArrowDown") return;
            e.preventDefault();
            const day = cal.querySelector('[aria-selected="true"]') || cal.querySelector("[data-today]") || cal.querySelector(".pick-day");
            if (day) day.focus();
        }

        input.addEventListener("focus", open);
        input.addEventListener("mousedown", () => { if (!openPop) open(); });
        input.addEventListener("blur", () => {
            const d = parseISO(input.value);
            if (d) input.value = iso(d);
        });
    }

    document.addEventListener("keydown", (e) => {
        if (!openPop) return;
        if (e.key === "Escape") {
            e.stopPropagation();
            const el = openPop.input;
            closePop();
            el.focus();
            return;
        }
        if (openPop.onKey) openPop.onKey(e);
    });

    document.addEventListener("pointerdown", (e) => {
        if (openPop && !openPop.el.parentNode.contains(e.target)) closePop();
    });

    document.addEventListener("focusin", (e) => {
        if (openPop && !openPop.el.parentNode.contains(e.target)) closePop();
    });

    function enhance(root) {
        root.querySelectorAll('input[type="time"]:not([data-pick])').forEach((el) => {
            el.setAttribute("data-pick", "time");
            enhanceTime(el);
        });
        root.querySelectorAll('input[type="date"]:not([data-pick])').forEach((el) => {
            el.setAttribute("data-pick", "date");
            enhanceDate(el);
        });
    }

    enhance(document);
    // Forms arrive as fragments too, so upgrade whatever gets swapped in.
    document.addEventListener("fragment", (e) => enhance(e.target || document));
})();
