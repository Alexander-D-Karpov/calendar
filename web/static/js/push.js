"use strict";

(() => {
    const root = document.querySelector("[data-push]");
    if (!root) return;

    const enable = root.querySelector("[data-push-enable]");
    const on = root.querySelector("[data-push-on]");
    const unsupported = root.querySelector("[data-push-unsupported]");
    const denied = root.querySelector("[data-push-denied]");
    const errorBox = root.querySelector("[data-push-error]");

    const fail = (text) => {
        errorBox.textContent = text;
        errorBox.hidden = false;
    };

    const b64 = (buf) => {
        const bytes = new Uint8Array(buf);
        let s = "";
        for (const b of bytes) s += String.fromCharCode(b);
        return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
    };

    const keyBytes = (key) => {
        const padded = (key + "=".repeat((4 - (key.length % 4)) % 4)).replace(/-/g, "+").replace(/_/g, "/");
        const raw = atob(padded);
        const out = new Uint8Array(raw.length);
        for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
        return out;
    };

    if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) {
        unsupported.hidden = false;
        return;
    }
    if (Notification.permission === "denied") {
        denied.hidden = false;
        return;
    }

    const send = async (sub) => {
        const body = new URLSearchParams({
            endpoint: sub.endpoint,
            p256dh: b64(sub.getKey("p256dh")),
            auth: b64(sub.getKey("auth")),
        });
        const res = await fetch(root.dataset.action, {
            method: "POST",
            headers: {
                "X-Fragment": "1",
                "X-CSRF-Token": root.dataset.csrf,
                "Content-Type": "application/x-www-form-urlencoded",
            },
            body,
            credentials: "same-origin",
        });
        if (!res.ok && res.status !== 204) throw new Error("subscribe failed");
    };

    (async () => {
        try {
            const reg = await navigator.serviceWorker.register("/sw.js", { scope: "/" });
            const existing = await reg.pushManager.getSubscription();
            if (existing && Notification.permission === "granted") {
                on.hidden = false;
                return;
            }
            enable.hidden = false;
            enable.addEventListener("click", async () => {
                enable.disabled = true;
                try {
                    const perm = await Notification.requestPermission();
                    if (perm !== "granted") {
                        denied.hidden = false;
                        return;
                    }
                    const sub = existing || await reg.pushManager.subscribe({
                        userVisibleOnly: true,
                        applicationServerKey: keyBytes(root.dataset.key),
                    });
                    await send(sub);
                    window.location.reload();
                } catch (err) {
                    fail("Could not enable notifications: " + err.message);
                } finally {
                    enable.disabled = false;
                }
            });
        } catch (err) {
            fail("Service worker registration failed: " + err.message);
        }
    })();
})();