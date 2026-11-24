package apierr

import (
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// ProblemDetails implements RFC 7807 Problem Details for HTTP APIs.
type ProblemDetails struct {
	Type     string `json:"type" example:"https://engram.dev/errors/not-found"`
	Title    string `json:"title" example:"Resource Not Found"`
	Status   int    `json:"status" example:"404"`
	Detail   string `json:"detail" example:"Memory with key 'auth-flow' was not found"`
	Instance string `json:"instance,omitempty"`
	Code     string `json:"code,omitempty" example:"MEMORY_NOT_FOUND"`
}

func (p ProblemDetails) Error() string {
	return fmt.Sprintf("[%d %s] %s: %s", p.Status, p.Code, p.Title, p.Detail)
}

func (p ProblemDetails) GetStatus() int {
	return p.Status
}

// Ensure interface compatibility with Huma StatusError
var _ huma.StatusError = (*ProblemDetails)(nil)

func NotFound(code, detail string) ProblemDetails {
	return ProblemDetails{
		Type:   "https://engram.dev/errors/not-found",
		Title:  "Not Found",
		Status: http.StatusNotFound,
		Code:   code,
		Detail: detail,
	}
}

func Conflict(code, detail string) ProblemDetails {
	return ProblemDetails{
		Type:   "https://engram.dev/errors/conflict",
		Title:  "Conflict",
		Status: http.StatusConflict,
		Code:   code,
		Detail: detail,
	}
}

func BadRequest(code, detail string) ProblemDetails {
	return ProblemDetails{
		Type:   "https://engram.dev/errors/bad-request",
		Title:  "Bad Request",
		Status: http.StatusBadRequest,
		Code:   code,
		Detail: detail,
	}
}

func Internal(detail string) ProblemDetails {
	return ProblemDetails{
		Type:   "https://engram.dev/errors/internal",
		Title:  "Internal Server Error",
		Status: http.StatusInternalServerError,
		Code:   "INTERNAL_ERROR",
		Detail: detail,
	}
}
