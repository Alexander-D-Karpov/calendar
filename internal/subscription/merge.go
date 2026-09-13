package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"slices"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
	"github.com/Alexander-D-Karpov/calendar/internal/transfer"
)

func itemHash(e domain.Event, modified time.Time) []byte {
	h := sha256.New()
	h.Write([]byte(e.Title))
	h.Write([]byte{0})
	h.Write([]byte(e.Body))
	h.Write([]byte{0})
	h.Write([]byte(e.Location))
	h.Write([]byte{0})
	h.Write([]byte(e.URL))
	h.Write([]byte{0})
	h.Write([]byte(e.RRule))
	h.Write([]byte{0})
	h.Write([]byte(e.TZ))
	h.Write([]byte{0})
	h.Write([]byte(e.Status + e.Transparency + e.Visibility))
	var b [8]byte
	for _, t := range append([]time.Time{e.Start, e.End}, append(slices.Clone(e.RDate), e.ExDate...)...) {
		binary.BigEndian.PutUint64(b[:], uint64(t.UTC().Unix()))
		h.Write(b[:])
	}
	for _, m := range e.Reminders {
		binary.BigEndian.PutUint64(b[:], uint64(m))
		h.Write(b[:])
	}
	if e.AllDay {
		h.Write([]byte{1})
	}
	binary.BigEndian.PutUint64(b[:], uint64(e.Sequence))
	h.Write(b[:])
	binary.BigEndian.PutUint64(b[:], uint64(modified.UTC().Unix()))
	h.Write(b[:])
	return h.Sum(nil)[:16]
}

func key(uid string, rid *time.Time) string {
	if rid == nil {
		return uid
	}
	return uid + "\x00" + rid.UTC().Format(time.RFC3339)
}

func (s *Service) merge(ctx context.Context, sub domain.Subscription, set transfer.Set, _ []byte) error {
	type incoming struct {
		event    domain.Event
		modified time.Time
		master   string
	}
	wanted := map[string]incoming{}
	var order []string
	for _, c := range set.Calendars {
		for _, sr := range c.Series {
			uid := sr.Master.UID
			if uid == "" {
				uid = "fp:" + string(domain.EventFingerprint(sr.Master))
			}
			k := key(uid, nil)
			if _, dup := wanted[k]; dup {
				continue
			}
			m := sr.Master
			m.UID = uid
			wanted[k] = incoming{event: m, modified: sr.Modified}
			order = append(order, k)
			for _, ov := range sr.Overrides {
				ok := key(uid, ov.RecurrenceID)
				if _, dup := wanted[ok]; dup {
					continue
				}
				o := ov
				o.UID = uid
				wanted[ok] = incoming{event: o, modified: sr.Modified, master: k}
				order = append(order, ok)
			}
		}
	}
	ctx = domain.WithOrigin(ctx, domain.OriginSubscription)
	return s.repo.WithImport(ctx, sub.OwnerID, func(tx store.ImportTx) error {
		current, err := tx.CalendarEvents(ctx, sub.CalendarID)
		if err != nil {
			return err
		}
		have := make(map[string]domain.Event, len(current))
		for _, e := range current {
			have[key(e.UID, e.RecurrenceID)] = e
		}
		ids := map[string]domain.ID{}
		now := s.now()
		for _, k := range order {
			in := wanted[k]
			e := in.event
			e.OwnerID, e.CalendarID, e.ReadOnly = sub.OwnerID, sub.CalendarID, true
			if e.TZ == "" && !e.AllDay {
				e.TZ = "UTC"
			}
			if e.Reminders == nil {
				e.Reminders = []int{}
			}
			if !e.End.After(e.Start) && e.AllDay {
				e.End = e.Start.AddDate(0, 0, 1)
			}
			e.SourceHash = itemHash(e, in.modified)
			if in.master != "" {
				sid, ok := ids[in.master]
				if !ok {
					continue
				}
				rid := *e.RecurrenceID
				e.SeriesID, e.RecurrenceID = &sid, &rid
				e.RRule, e.RDate, e.ExDate = "", nil, nil
			}
			cur, exists := have[k]
			if !exists {
				e.ID, e.Version = domain.NewID(), 1
				saved, err := tx.Events().Insert(ctx, e)
				if err != nil {
					continue
				}
				ids[k] = saved.ID
				continue
			}
			ids[k] = cur.ID
			delete(have, k)
			if slices.Equal(cur.SourceHash, e.SourceHash) {
				continue
			}
			e.ID, e.Version, e.CreatedAt = cur.ID, cur.Version, cur.CreatedAt
			if _, err := tx.Events().Update(ctx, e); err != nil {
				continue
			}
		}
		for _, e := range have {
			if e.SeriesID != nil {
				continue
			}
			if err := tx.Events().Delete(ctx, e, now); err != nil {
				return err
			}
		}
		return nil
	})
}
