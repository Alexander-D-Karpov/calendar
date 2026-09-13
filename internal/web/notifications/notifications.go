package notifications

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/push"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

const path = "/settings/notifications"

type Store interface {
	AddPushSubscription(ctx context.Context, p domain.PushSubscription) (domain.PushSubscription, error)
	PushSubscriptions(ctx context.Context, user domain.ID) ([]domain.PushSubscription, error)
	PushSubscription(ctx context.Context, user, id domain.ID) (domain.PushSubscription, error)
	DeletePushSubscription(ctx context.Context, user, id domain.ID) error
}

type Deps struct {
	Site   *web.Server
	Store  Store
	Sender *push.Sender
}

type Module struct {
	site   *web.Server
	store  Store
	sender *push.Sender
}

type pageView struct {
	Enabled   bool
	PublicKey string
	Devices   []domain.PushSubscription
}

func New(d Deps) *Module {
	return &Module{site: d.Site, store: d.Store, sender: d.Sender}
}

func (m *Module) Routes(mux *http.ServeMux) {
	h, user := m.site.Handle, m.site.RequireUser
	mux.Handle("GET "+path, h(m.page, user))
	mux.Handle("POST "+path+"/subscribe", h(m.subscribe, user))
	mux.Handle("POST "+path+"/{id}/test", h(m.test, user))
	mux.Handle("POST "+path+"/{id}/delete", h(m.remove, user))
}

func (m *Module) page(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	devices, err := m.store.PushSubscriptions(r.Context(), v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	pv := pageView{Enabled: m.sender != nil, Devices: devices}
	if m.sender != nil {
		pv.PublicKey = m.sender.PublicKey()
	}
	m.site.Render(w, r, http.StatusOK, "settings/notifications", v.Page("settings/notifications", "Notifications", pv))
}

func (m *Module) subscribe(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	endpoint := f.Get("endpoint")
	if !strings.HasPrefix(endpoint, "https://") || f.Get("p256dh") == "" || f.Get("auth") == "" {
		m.site.Fail(w, r, domain.ErrInvalid)
		return
	}
	_, err = m.store.AddPushSubscription(r.Context(), domain.PushSubscription{
		ID:        domain.NewID(),
		UserID:    v.User.ID,
		Endpoint:  endpoint,
		P256dh:    f.Get("p256dh"),
		Auth:      f.Get("auth"),
		UserAgent: web.Clip(r.UserAgent(), 512),
	})
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	m.site.Flash(w, r, "ok", "Notifications are on for this device.")
	m.site.Finish(w, r, path)
}

func (m *Module) test(w http.ResponseWriter, r *http.Request) {
	v, sub, ok := m.load(w, r)
	if !ok {
		return
	}
	if m.sender == nil {
		m.site.Flash(w, r, "error", "Web push is not configured on this server.")
		m.site.Redirect(w, r, path)
		return
	}
	err := m.sender.Send(r.Context(), sub, push.Message{
		Title: "Calendar",
		Body:  "Test notification for " + v.User.Name() + ".",
		URL:   m.site.Config().App.URL("/"),
		Tag:   "test",
	})
	switch {
	case err == nil:
		m.site.Flash(w, r, "ok", "Test notification sent.")
	case errors.Is(err, push.ErrGone):
		_ = m.store.DeletePushSubscription(r.Context(), v.User.ID, sub.ID)
		m.site.Flash(w, r, "error", "That device is no longer subscribed, so it was removed.")
	default:
		m.site.Flash(w, r, "error", "The push service refused the message.")
	}
	m.site.Redirect(w, r, path)
}

func (m *Module) remove(w http.ResponseWriter, r *http.Request) {
	v, sub, ok := m.load(w, r)
	if !ok {
		return
	}
	if err := m.store.DeletePushSubscription(r.Context(), v.User.ID, sub.ID); err != nil {
		m.site.Fail(w, r, err)
		return
	}
	m.site.Flash(w, r, "ok", "Device removed.")
	m.site.Redirect(w, r, path)
}

func (m *Module) load(w http.ResponseWriter, r *http.Request) (web.Viewer, domain.PushSubscription, bool) {
	id, err := domain.ParseID(r.PathValue("id"))
	if err != nil {
		m.site.Fail(w, r, domain.ErrNotFound)
		return web.Viewer{}, domain.PushSubscription{}, false
	}
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, domain.PushSubscription{}, false
	}
	sub, err := m.store.PushSubscription(r.Context(), v.User.ID, id)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, domain.PushSubscription{}, false
	}
	return v, sub, true
}
