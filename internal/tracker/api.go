package tracker

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

func NewHandler(path string) http.Handler {
	mux := http.NewServeMux()
	load := func(w http.ResponseWriter) (Dataset, bool) {
		d, err := Load(path)
		if err != nil {
			respond(w, 503, map[string]string{"error": "Данные трекера временно недоступны"})
			return d, false
		}
		return d, true
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := load(w); ok {
			respond(w, 200, map[string]string{"status": "ok"})
		}
	})
	mux.HandleFunc("GET /api/issues", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		s := Search{Project: q.Get("project"), Status: q.Get("status"), Priority: q.Get("priority"), AssigneeID: q.Get("assigneeId"), Query: q.Get("query")}
		for key, values := range q {
			if len(values) != 1 {
				respond(w, 400, map[string]string{"error": "Повторяющийся параметр"})
				return
			}
			switch key {
			case "project", "status", "priority", "assigneeId", "query", "page", "pageSize":
			default:
				respond(w, 400, map[string]string{"error": "Неизвестный параметр"})
				return
			}
		}
		for key, dest := range map[string]*int{"page": &s.Page, "pageSize": &s.PageSize} {
			if q.Has(key) {
				v, err := strconv.Atoi(q.Get(key))
				if err != nil || v < 1 {
					respond(w, 400, map[string]string{"error": "Некорректная пагинация"})
					return
				}
				*dest = v
			}
		}
		if err := s.Validate(); err != nil {
			respond(w, 400, map[string]string{"error": err.Error()})
			return
		}
		d, ok := load(w)
		if !ok {
			return
		}
		result, _ := d.Search(s)
		respond(w, 200, result)
	})
	get := func(comments bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			key := r.PathValue("key")
			if !KeyPattern.MatchString(key) {
				respond(w, 400, map[string]string{"error": "Некорректный ключ задачи"})
				return
			}
			d, ok := load(w)
			if !ok {
				return
			}
			var value any
			var err error
			if comments {
				value, err = d.CommentsFor(key)
			} else {
				value, err = d.Get(key)
			}
			if errors.Is(err, ErrNotFound) {
				respond(w, 404, map[string]string{"error": err.Error()})
				return
			}
			respond(w, 200, value)
		}
	}
	mux.HandleFunc("GET /api/issues/{key}", get(false))
	mux.HandleFunc("GET /api/issues/{key}/comments", get(true))
	return mux
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
