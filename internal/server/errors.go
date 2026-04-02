package server

import (
	"encoding/json"
	"net/http"
)

// APIError is the unified envelope returned for all error responses.
type APIError struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
	Code    int    `json:"code"`
}

func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(APIError{
		Error:   http.StatusText(code),
		Message: message,
		Code:    code,
	})
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(payload)
}
