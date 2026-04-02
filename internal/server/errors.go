package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// APIError is the unified envelope returned for all error responses.
type APIError struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
	Code    int    `json:"code"`
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeErrorCode(w, code, defaultErrorCode(code), message)
}

func writeErrorCode(w http.ResponseWriter, code int, errCode, message string) {
	if strings.TrimSpace(errCode) == "" {
		errCode = defaultErrorCode(code)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(APIError{
		Error:   errCode,
		Message: message,
		Code:    code,
	})
}

func defaultErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusUnprocessableEntity:
		return "unprocessable_entity"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusInternalServerError:
		return "internal_error"
	default:
		name := strings.ToLower(strings.TrimSpace(http.StatusText(status)))
		if name == "" {
			return "error"
		}
		return strings.ReplaceAll(name, " ", "_")
	}
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
