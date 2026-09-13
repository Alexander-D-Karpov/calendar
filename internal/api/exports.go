package api

import (
	"io"
	"mime"
	"net/http"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/exporter"
)

func readAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, maxUploadMemory*4))
}

func calendarIDs(r *http.Request) ([]domain.ID, error) {
	var v domain.ValidationError
	var out []domain.ID
	for _, raw := range queryList(r.URL.Query(), "calendar_id") {
		id, err := domain.ParseID(raw)
		if err != nil {
			v.Addf("calendar_id", "unknown calendar %q", raw)
			continue
		}
		out = append(out, id)
	}
	return out, v.Err()
}

func sendExport(w http.ResponseWriter, f exporter.File) {
	h := w.Header()
	h.Set("Content-Type", f.ContentType)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(f.Body)
}

func exportWith(d Deps, fn func(Deps, *http.Request, []domain.ID) (exporter.File, error)) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ids, err := calendarIDs(r)
		if err != nil {
			return err
		}
		f, err := fn(d, r, ids)
		if err != nil {
			return err
		}
		sendExport(w, f)
		return nil
	}
}

func exportICS(d Deps) apiFunc {
	return exportWith(d, func(d Deps, r *http.Request, ids []domain.ID) (exporter.File, error) {
		return d.Export.ICS(r.Context(), ownerID(r), ids)
	})
}

func exportCSV(d Deps) apiFunc {
	return exportWith(d, func(d Deps, r *http.Request, ids []domain.ID) (exporter.File, error) {
		return d.Export.CSV(r.Context(), ownerID(r), ids)
	})
}

func exportYAML(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		f, err := d.Export.YAML(r.Context(), ownerID(r))
		if err != nil {
			return err
		}
		sendExport(w, f)
		return nil
	}
}
