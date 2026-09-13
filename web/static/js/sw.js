"use strict";

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));

self.addEventListener("push", (e) => {
    let d = {};
    try {
        d = e.data ? e.data.json() : {};
    } catch (_) {
        d = {};
    }
    e.waitUntil(self.registration.showNotification(d.title || "Calendar", {
        body: d.body || "",
        tag: d.tag || "calendar",
        data: { url: d.url || "/" },
        badge: "/static/img/favicon.svg",
        icon: "/static/img/favicon.svg",
    }));
});

self.addEventListener("notificationclick", (e) => {
    e.notification.close();
    const url = (e.notification.data && e.notification.data.url) || "/";
    e.waitUntil(self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((list) => {
        for (const c of list) {
            if (c.url === url && "focus" in c) return c.focus();
        }
        return self.clients.openWindow(url);
    }));
});