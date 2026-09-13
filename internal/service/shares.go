package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const (
	shareTokenBytes = 32
	shareTokenLen   = 43
)

type ShareRepo interface {
	ListShares(ctx context.Context, owner domain.ID) ([]domain.Share, error)
	CountShares(ctx context.Context, owner domain.ID) (int, error)
	Share(ctx context.Context, owner, id domain.ID) (domain.Share, error)
	ShareByTokenHash(ctx context.Context, hash []byte) (domain.Share, error)
	CreateShare(ctx context.Context, s domain.Share) (domain.Share, error)
	UpdateShare(ctx context.Context, owner, id domain.ID, fn func(*domain.Share) error) (domain.Share, error)
	TouchShare(ctx context.Context, id domain.ID, at time.Time) error
}

type Shares struct {
	repo  ShareRepo
	cals  CalendarRepo
	keys  *crypto.KeyRing
	clock clock.Clock
}

func NewShares(repo ShareRepo, cals CalendarRepo, keys *crypto.KeyRing, clk clock.Clock) *Shares {
	if clk == nil {
		clk = clock.New()
	}
	return &Shares{repo: repo, cals: cals, keys: keys, clock: clk}
}

func (s *Shares) List(ctx context.Context, owner domain.ID) ([]domain.Share, error) {
	list, err := s.repo.ListShares(ctx, owner)
	if err != nil {
		return nil, err
	}
	for i := range list {
		s.reveal(&list[i])
	}
	return list, nil
}

func (s *Shares) Get(ctx context.Context, owner, id domain.ID) (domain.Share, error) {
	sh, err := s.repo.Share(ctx, owner, id)
	if err != nil {
		return domain.Share{}, err
	}
	s.reveal(&sh)
	return sh, nil
}

func (s *Shares) Create(ctx context.Context, owner domain.ID, p domain.SharePatch) (domain.Share, error) {
	n, err := s.repo.CountShares(ctx, owner)
	if err != nil {
		return domain.Share{}, err
	}
	if n >= domain.MaxShares {
		return domain.Share{}, fmt.Errorf("%w: an account can have at most %d share links", domain.ErrConflict, domain.MaxShares)
	}
	sh := domain.Share{
		ID:        domain.NewID(),
		OwnerID:   owner,
		Detail:    domain.DetailTitles,
		TZMode:    domain.ShareTZOwner,
		ShowSleep: true,
		Calendars: []domain.ID{},
		Version:   1,
	}
	if err := s.apply(ctx, &sh, p, true); err != nil {
		return domain.Share{}, err
	}
	s.issue(&sh)
	out, err := s.repo.CreateShare(ctx, sh)
	if err != nil {
		return domain.Share{}, err
	}
	out.Token = sh.Token
	return out, nil
}

func (s *Shares) Update(ctx context.Context, owner, id domain.ID, p domain.SharePatch, ifMatch string) (domain.Share, error) {
	out, err := s.repo.UpdateShare(ctx, owner, id, func(sh *domain.Share) error {
		if !sh.Active() {
			return fmt.Errorf("%w: the link is revoked", domain.ErrConflict)
		}
		if err := domain.CheckIfMatch(ifMatch, sh.ETag()); err != nil {
			return err
		}
		return s.apply(ctx, sh, p, false)
	})
	if err != nil {
		return domain.Share{}, err
	}
	s.reveal(&out)
	return out, nil
}

func (s *Shares) Regenerate(ctx context.Context, owner, id domain.ID) (domain.Share, error) {
	out, err := s.repo.UpdateShare(ctx, owner, id, func(sh *domain.Share) error {
		if !sh.Active() {
			return fmt.Errorf("%w: the link is revoked", domain.ErrConflict)
		}
		s.issue(sh)
		return nil
	})
	if err != nil {
		return domain.Share{}, err
	}
	s.reveal(&out)
	return out, nil
}

func (s *Shares) Revoke(ctx context.Context, owner, id domain.ID) error {
	_, err := s.repo.UpdateShare(ctx, owner, id, func(sh *domain.Share) error {
		if sh.Active() {
			now := s.clock.Now().UTC()
			sh.RevokedAt = &now
		}
		return nil
	})
	return err
}

func (s *Shares) Open(ctx context.Context, token string, count bool) (domain.Share, error) {
	if !ValidShareToken(token) {
		return domain.Share{}, domain.ErrNotFound
	}
	sh, err := s.repo.ShareByTokenHash(ctx, crypto.HashToken(token))
	if err != nil {
		return domain.Share{}, err
	}
	if count {
		if err := s.repo.TouchShare(ctx, sh.ID, s.clock.Now().UTC()); err != nil {
			return domain.Share{}, err
		}
	}
	return sh, nil
}

func (s *Shares) apply(ctx context.Context, sh *domain.Share, p domain.SharePatch, isNew bool) error {
	var v domain.ValidationError
	if x, ok := p.Name.Value(&v, "name"); ok {
		sh.Name = strings.TrimSpace(x)
	}
	switch {
	case isNew:
		kind := strings.TrimSpace(p.View.V)
		if slices.Contains(domain.ShareViews, kind) {
			sh.View = kind
		} else {
			v.Addf("view", "must be one of %s", strings.Join(domain.ShareViews, ", "))
		}
		if d, ok := parseDate(p.Period.V); ok {
			sh.Period = d
		} else {
			v.Add("period", "must be a date like 2026-09-07")
		}
	case p.View.Set || p.Period.Set:
		v.Add("view", "cannot be changed, create a new link for another period")
	}
	choice(&v, p.Detail, "detail", &sh.Detail, domain.ShareDetails...)
	choice(&v, p.TZMode, "tz_mode", &sh.TZMode, domain.ShareTZModes...)
	if x, ok := p.IncludeTodos.Value(&v, "include_todos"); ok {
		sh.IncludeTodos = x
	}
	if x, ok := p.ShowSleep.Value(&v, "show_sleep"); ok {
		sh.ShowSleep = x
	}
	if x, ok := p.Calendars.Value(&v, "calendars"); ok {
		ids, known, err := s.calendarIDs(ctx, sh.OwnerID, x)
		if err != nil {
			return err
		}
		if known {
			sh.Calendars = ids
		} else {
			v.Add("calendars", "contains an unknown calendar")
		}
	}
	v.Length("name", sh.Name, 1, 100)
	if len(sh.Calendars) == 0 && !sh.IncludeTodos && !hasField(&v, "calendars") {
		v.Add("calendars", "choose at least one calendar or include todos")
	}
	return v.Err()
}

func (s *Shares) calendarIDs(ctx context.Context, owner domain.ID, raw []string) ([]domain.ID, bool, error) {
	list, err := s.cals.ListCalendars(ctx, owner)
	if err != nil {
		return nil, false, err
	}
	known := make(map[domain.ID]bool, len(list))
	for _, c := range list {
		known[c.ID] = true
	}
	out := []domain.ID{}
	for _, r := range raw {
		id, err := domain.ParseID(strings.TrimSpace(r))
		if err != nil || !known[id] {
			return nil, false, nil
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, true, nil
}

func (s *Shares) issue(sh *domain.Share) {
	tok := crypto.RandomToken(shareTokenBytes)
	sh.Token, sh.TokenHash = tok, crypto.HashToken(tok)
	sh.TokenEnc = s.keys.SealString(tok, shareAAD(sh.ID))
}

func (s *Shares) reveal(sh *domain.Share) {
	if !sh.Active() || len(sh.TokenEnc) == 0 {
		return
	}
	if tok, err := s.keys.OpenString(sh.TokenEnc, shareAAD(sh.ID)); err == nil {
		sh.Token = tok
	}
}

func shareAAD(id domain.ID) []byte {
	return crypto.AAD("share_token", id.String())
}

func ValidShareToken(s string) bool {
	if len(s) != shareTokenLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
