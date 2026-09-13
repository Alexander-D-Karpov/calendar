package account

import (
	"context"
	"net/http"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/google"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

type Connector interface {
	Connect(ctx context.Context, user domain.ID, identity domain.Identity, refresh string, scopes []string) error
	Disconnect(ctx context.Context, owner domain.ID, keep bool) error
}

type Pages struct {
	site     *web.Server
	auth     *auth.Service
	guard    *auth.Guard
	clock    clock.Clock
	cfg      *config.Config
	settings *service.Settings
	google   *google.OAuth
	sync     Connector
}

func New(site *web.Server, settings *service.Settings, g *google.OAuth, sync Connector) *Pages {
	return &Pages{
		site: site, auth: site.Auth(), guard: site.Guard(), clock: site.Clock(), cfg: site.Config(),
		settings: settings, google: g, sync: sync,
	}
}

func (a *Pages) Routes(mux *http.ServeMux) {
	h, user, fresh := a.site.Handle, a.site.RequireUser, a.site.RequireFresh
	mux.Handle("GET /login", h(a.loginPage))
	mux.Handle("POST /login", h(a.login))
	mux.Handle("GET /register", h(a.registerPage))
	mux.Handle("POST /register", h(a.register))
	mux.Handle("POST /logout", h(a.logout, user))
	mux.Handle("GET /verify", h(a.verifyPage))
	mux.Handle("POST /verify/resend", h(a.resendVerify, user))
	mux.Handle("GET /forgot", h(a.forgotPage))
	mux.Handle("POST /forgot", h(a.forgot))
	mux.Handle("GET /reset", h(a.resetPage))
	mux.Handle("POST /reset", h(a.reset))
	mux.Handle("GET /reauth", h(a.reauthPage, user))
	mux.Handle("POST /reauth", h(a.reauth, user))
	mux.Handle("POST /auth/google", h(a.googleStart))
	mux.Handle("GET "+google.CallbackPath, h(a.googleCallback))
	mux.Handle("GET /settings", h(web.RedirectTo(profilePath), user))
	mux.Handle("GET "+profilePath, h(a.profilePage, user))
	mux.Handle("POST "+profilePath, h(a.saveProfile, user))
	mux.Handle("GET "+securityPath, h(a.securityPage, user))
	mux.Handle("POST "+securityPath+"/password", h(a.changePassword, user))
	mux.Handle("POST "+securityPath+"/google/unlink", h(a.unlinkGoogle, user, fresh))
	mux.Handle("GET /settings/sessions", h(a.sessionsPage, user))
	mux.Handle("POST /settings/sessions/{id}/revoke", h(a.revokeSession, user))
	mux.Handle("POST /settings/sessions/revoke-others", h(a.revokeOtherSessions, user))
	mux.Handle("GET /settings/tokens", h(a.tokensPage, user))
	mux.Handle("POST /settings/tokens", h(a.createToken, user, fresh))
	mux.Handle("POST /settings/tokens/{id}/revoke", h(a.revokeToken, user))
}
