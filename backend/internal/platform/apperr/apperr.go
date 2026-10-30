package apperr

import (
	"errors"
	"net/http"
)

type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func Invalid(message string) *Error {
	return New(http.StatusUnprocessableEntity, "VALIDATION_ERROR", message)
}

func NotFound(message string) *Error {
	return New(http.StatusNotFound, "NOT_FOUND", message)
}

func Conflict(code, message string) *Error {
	return New(http.StatusConflict, code, message)
}

func Forbidden() *Error {
	return New(http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok")
}

func Unauthorized(message string) *Error {
	return New(http.StatusUnauthorized, "UNAUTHORIZED", message)
}

func As(err error) (*Error, bool) {
	var target *Error
	ok := errors.As(err, &target)
	return target, ok
}
