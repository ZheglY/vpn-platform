package httperror

import (
	"encoding/json"
	"net/http"

	"github.com/ZheglY/vpn-platform/internal/platform/requestid"
)

type Response struct {
	Code      string       `json:"code"`
	Message   string       `json:"message"`
	RequestID string       `json:"request_id"`
	Fields    []FieldError `json:"fields,omitempty"`
}

type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func Write(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Response{
		Code:      code,
		Message:   message,
		RequestID: requestid.FromRequest(r),
	})
}
