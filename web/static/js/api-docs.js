"use strict";

(() => {
    const root = document.getElementById("swagger-ui");
    if (!root || typeof window.SwaggerUIBundle !== "function") return;
    const csrf = document.querySelector('meta[name="csrf-token"]')?.content || "";

    window.SwaggerUIBundle({
        url: root.dataset.spec,
        domNode: root,
        deepLinking: true,
        validatorUrl: null,
        displayRequestDuration: true,
        persistAuthorization: false,
        docExpansion: "list",
        defaultModelsExpandDepth: 1,
        requestInterceptor: (req) => {
            const target = new URL(req.url, window.location.href);
            if (target.origin === window.location.origin) {
                req.credentials = "same-origin";
                if (csrf) req.headers["X-CSRF-Token"] = csrf;
            }
            return req;
        },
    });
})();