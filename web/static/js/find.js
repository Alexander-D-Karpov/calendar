"use strict";

(() => {
    const MATCHES = ".block, .bar, .agenda-item, .hit, .todo-row, .table tbody tr";

    let bar = null;
    let input = null;

    const container = () => document.querySelector("[data-findable]");

    const apply = (q) => {
        const root = container();
        if (!root) return;
        const needle = q.trim().toLowerCase();
        root.querySelectorAll(MATCHES).forEach((el) => {
            el.classList.toggle("dim", needle !== "" && !el.textContent.toLowerCase().includes(needle));
        });
    };

    const close = () => {
        if (!bar) return;
        input.value = "";
        apply("");
        bar.hidden = true;
    };

    const jump = () => {
        const root = container();
        let q = input.value;
        if (root && root.dataset.from && root.dataset.to) {
            q += " after:" + root.dataset.from + " before:" + root.dataset.to;
        }
        window.location.assign("/search?" + new URLSearchParams({ q }).toString());
    };

    const build = () => {
        bar = document.createElement("div");
        bar.className = "cal-findbar";
        bar.hidden = true;
        input = document.createElement("input");
        input.type = "search";
        input.autocomplete = "off";
        input.placeholder = "Find on this page";
        input.setAttribute("aria-label", "Find on this page");
        input.dataset.findInput = "";
        bar.append(input);
        input.addEventListener("input", () => apply(input.value));
        input.addEventListener("keydown", (e) => {
            if (e.key === "Escape") {
                e.preventDefault();
                close();
                return;
            }
            if (e.key !== "Enter") return;
            e.preventDefault();
            jump();
        });
        const header = document.querySelector("body > .site-header");
        if (header) header.after(bar);
        else document.body.prepend(bar);
    };

    const open = () => {
        if (!container()) return;
        if (!bar) build();
        bar.hidden = false;
        input.focus();
        input.select();
    };

    document.querySelectorAll("[data-find-open]").forEach((button) => {
        button.addEventListener("click", () => (bar && !bar.hidden ? close() : open()));
    });

    document.addEventListener("keydown", (e) => {
        if (!(e.ctrlKey || e.metaKey) || e.key.toLowerCase() !== "f" || e.shiftKey || e.defaultPrevented) return;
        if (document.querySelector("dialog[open]")) return;
        if (!container()) return;
        e.preventDefault();
        open();
    });
})();
