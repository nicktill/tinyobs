package httpx

import (
	"encoding/json"
	"log"
	"net/http"
)

// RespondJSON writes a JSON response with the given status code and data.
func RespondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Failed to encode JSON response: %v", err)
	}
}

// ErrorResponse represents an error response.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// RespondError writes an error response with the given status code and error message.
func RespondError(w http.ResponseWriter, status int, err error) {
	response := ErrorResponse{
		Error:   http.StatusText(status),
		Message: err.Error(),
	}
	RespondJSON(w, status, response)
}
// RespondErrorString writes an error response with the given status code and error message string.
func RespondErrorString(w http.ResponseWriter, status int, message string) {
	response := ErrorResponse{
		Error:   http.StatusText(status),
		Message: message,
	}
	RespondJSON(w, status, response)
}