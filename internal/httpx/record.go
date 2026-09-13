package httpx

import "net/http"

type ResponseInfo interface {
	StatusCode() int
	Size() int64
	Hijacked() bool
}

func Record(w http.ResponseWriter) (http.ResponseWriter, ResponseInfo) {
	rec := &responseRecorder{ResponseWriter: w}
	return rec, rec
}

func (r *responseRecorder) StatusCode() int {
	switch {
	case r.hijacked:
		return http.StatusSwitchingProtocols
	case r.status == 0:
		return http.StatusOK
	}
	return r.status
}

func (r *responseRecorder) Size() int64 {
	return r.bytes
}

func (r *responseRecorder) Hijacked() bool {
	return r.hijacked
}
