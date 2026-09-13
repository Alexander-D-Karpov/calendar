package api

import (
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/search"
)

type hitJSON struct {
	Kind      string     `json:"kind"`
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Snippet   string     `json:"snippet"`
	Container string     `json:"container"`
	AllDay    bool       `json:"all_day"`
	Done      bool       `json:"done"`
	Start     *time.Time `json:"start"`
	End       *time.Time `json:"end"`
}

type searchResponse struct {
	Query      string    `json:"query"`
	Type       string    `json:"type"`
	Items      []hitJSON `json:"items"`
	NextOffset *int      `json:"next_offset"`
	Warnings   []string  `json:"warnings,omitempty"`
}

func runSearch(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		q := r.URL.Query()
		limit, err := queryInt(q, "limit", 0)
		if err != nil {
			return err
		}
		offset, err := queryInt(q, "offset", 0)
		if err != nil {
			return err
		}
		res, err := d.Search.Search(r.Context(), ownerID(r), search.Request{
			Raw: q.Get("q"), Type: q.Get("type"), Limit: limit, Offset: offset,
		})
		if err != nil {
			return err
		}
		out := searchResponse{Query: res.Query.Raw, Type: res.Query.Type, Items: make([]hitJSON, len(res.Hits)), Warnings: res.Warnings}
		for i, h := range res.Hits {
			out.Items[i] = hitJSON{
				Kind: h.Kind, ID: h.ID.String(), Title: h.Title, Snippet: h.Snippet, Container: h.Container,
				AllDay: h.AllDay, Done: h.Done, Start: utcPtr(h.Start), End: utcPtr(h.End),
			}
		}
		if res.More {
			next := res.Next()
			out.NextOffset = &next
		}
		writeJSON(w, http.StatusOK, out)
		return nil
	}
}
