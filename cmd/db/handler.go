package main

import (
	"encoding/json"
	"github.com/roman-mazur/architecture-practice-4-template/datastore"
	"io"
	"log"
	"net/http"
	"strings"
)

type Handler struct {
	db *datastore.Db
}

func NewHandler(db *datastore.Db) *Handler {
	return &Handler{db: db}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/db/"):
		key := strings.TrimPrefix(r.URL.Path, "/db/")
		switch r.Method {
		case http.MethodGet:
			log.Println("new GET request")
			h.handleGet(w, r, key)
		case http.MethodPost:
			log.Println("new POST request")
			h.handlePost(w, r, key)
		default:
			log.Printf("unknown method: %s", r.Method)
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, key string) {
	valueType := r.URL.Query().Get("type")
	if valueType == "" {
		valueType = "string"
	}

	switch valueType {
	case "int64":
		val, err := h.db.GetInt64(key)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		h.respondJSON(w, map[string]any{
			"key":   key,
			"value": val,
		})
	case "string":
		val, err := h.db.Get(key)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		h.respondJSON(w, map[string]any{
			"key":   key,
			"value": val,
		})
	default:
		http.Error(w, "invalid type", http.StatusBadRequest)
	}
}

func (h *Handler) handlePost(w http.ResponseWriter, r *http.Request, key string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}
	defer func() {
		log.Println(string(body))
		r.Body.Close()
	}()

	var input map[string]any
	if err := json.Unmarshal(body, &input); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	val, ok := input["value"]
	if !ok {
		http.Error(w, `"value" field missing`, http.StatusBadRequest)
		return
	}

	switch v := val.(type) {
	case string:
		if err := h.db.Put(key, v); err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
		}
	case float64:
		intVal := int64(v)
		if float64(intVal) != v {
			http.Error(w, "value must be int64 or string", http.StatusBadRequest)
			return
		}
		if err := h.db.PutInt64(key, intVal); err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
		}
	default:
		http.Error(w, "unsupported value type", http.StatusBadRequest)
	}
}

func (h *Handler) respondJSON(w http.ResponseWriter, data any) {
	log.Println("json encode response", data)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
