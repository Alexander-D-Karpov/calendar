package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/importer"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

const maxUploadMemory = 20 << 20

type importJSON struct {
	ID         string            `json:"id"`
	Source     string            `json:"source"`
	Filename   string            `json:"filename"`
	Status     string            `json:"status"`
	Preview    *importer.Preview `json:"preview"`
	Stats      *importer.Stats   `json:"stats"`
	Error      string            `json:"error,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	ExpiresAt  time.Time         `json:"expires_at"`
	FinishedAt *time.Time        `json:"finished_at"`
}

type importList struct {
	Items []importJSON `json:"items"`
}

type commitRequest struct {
	Targets []commitTarget `json:"targets"`
}

type commitTarget struct {
	Index  int    `json:"index"`
	Target string `json:"target"`
	Skip   bool   `json:"skip"`
}

func toImportJSON(im domain.Import, p *importer.Preview) importJSON {
	out := importJSON{
		ID: im.ID.String(), Source: im.Source, Filename: im.Filename, Status: im.Status, Preview: p,
		Error: im.Error, CreatedAt: im.CreatedAt.UTC(), ExpiresAt: im.ExpiresAt.UTC(), FinishedAt: utcPtr(im.FinishedAt),
	}
	if len(im.Stats) > 0 {
		var s importer.Stats
		if json.Unmarshal(im.Stats, &s) == nil {
			out.Stats = &s
		}
	}
	return out
}

func listImports(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		limit, err := queryInt(r.URL.Query(), "limit", 20)
		if err != nil {
			return err
		}
		list, err := d.Import.List(r.Context(), ownerID(r), min(max(limit, 1), 100))
		if err != nil {
			return err
		}
		items := make([]importJSON, len(list))
		for i, im := range list {
			items[i] = toImportJSON(im, nil)
		}
		writeJSON(w, http.StatusOK, importList{Items: items})
		return nil
	}
}

func getImport(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		im, p, err := d.Import.Get(r.Context(), ownerID(r), id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, toImportJSON(im, &p))
		return nil
	}
}

func createImport(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := r.ParseMultipartForm(maxUploadMemory); err != nil {
			return decodeError(err)
		}
		file, head, err := r.FormFile("file")
		if err != nil {
			var v domain.ValidationError
			v.Add("file", "is required, send the file as multipart/form-data")
			return v.Err()
		}
		defer file.Close()
		body, err := readAll(file)
		if err != nil {
			return err
		}
		im, p, err := d.Import.Upload(r.Context(), ownerID(r), web.SafeFilename(head), body)
		if err != nil {
			return err
		}
		w.Header().Set("Location", Prefix+"/imports/"+im.ID.String())
		writeJSON(w, http.StatusCreated, toImportJSON(im, &p))
		return nil
	}
}

func commitImport(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var body commitRequest
		if err := decodeJSON(r, &body); err != nil {
			return err
		}
		c := importer.Commit{}
		for _, t := range body.Targets {
			c.Targets = append(c.Targets, importer.Choice{Index: t.Index, Target: t.Target, Skip: t.Skip})
		}
		if _, err := d.Import.Commit(r.Context(), ownerID(r), id, c); err != nil {
			return err
		}
		im, p, err := d.Import.Get(r.Context(), ownerID(r), id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, toImportJSON(im, &p))
		return nil
	}
}
