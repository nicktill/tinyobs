// Package httpx provides HTTP response utilities.
// This package has been moved to pkg/httpx/response.go for better package organization.
// This file is kept for backward compatibility but re-exports from the new location.
package httpx

import (
	"net/http"
	
	httpxpkg "github.com/nicktill/tinyobs/pkg/httpx"
)

// RespondJSON writes a JSON response with the given status code and data.
func RespondJSON(w http.ResponseWriter, status int, data interface{}) {
	httpxpkg.RespondJSON(w, status, data)
}

// ErrorResponse represents an error response.
type ErrorResponse = httpxpkg.ErrorResponse

// RespondError writes an error response with the given status code and error message.
func RespondError(w http.ResponseWriter, status int, err error) {
	httpxpkg.RespondError(w, status, err)
}

// RespondErrorString writes an error response with the given status code and error message string.
func RespondErrorString(w http.ResponseWriter, status int, message string) {
	httpxpkg.RespondErrorString(w, status, message)
}

