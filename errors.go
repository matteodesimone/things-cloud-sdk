package thingscloud

import (
	"errors"
	"fmt"
	"net/http"
)

// Errors a caller can tell apart with errors.Is, to say what went wrong
// without parsing messages. ErrUnauthorized (client.go) is the fifth.
var (
	// ErrNotFound means the target of a write is not in the history.
	ErrNotFound = errors.New("thingscloud: object not found")
	// ErrWrongKind means the target of a task write is another kind of
	// object, such as an area or a tag.
	ErrWrongKind = errors.New("thingscloud: object of another kind")
	// ErrRecurring means the target of a task write is a recurring template
	// or instance, which the verified write scope excludes.
	ErrRecurring = errors.New("thingscloud: recurring task")
	// ErrUnverifiedTarget means the history does not say enough about the
	// target to write it safely: its events are ambiguous, of a future kind,
	// or lack the recurrence markers.
	ErrUnverifiedTarget = errors.New("thingscloud: write target cannot be verified")
	// ErrInvalidUUID means an identifier is not canonical Base58.
	ErrInvalidUUID = errors.New("thingscloud: invalid identifier")
	// ErrConflict means the server refused a commit whose ancestor is no
	// longer the head of the history (HTTP 409).
	ErrConflict = errors.New("thingscloud: history conflict")
)

// PreflightError is a Task7 write refused before posting, after reading the
// raw history: Reason is one of ErrNotFound, ErrWrongKind, ErrRecurring and
// ErrUnverifiedTarget.
type PreflightError struct {
	Target string
	Reason error
	detail string
}

func (e *PreflightError) Error() string {
	return fmt.Sprintf("Task7 write preflight: target %s %s", e.Target, e.detail)
}

func (e *PreflightError) Unwrap() error { return e.Reason }

// Is makes an HTTPError match ErrUnauthorized on 401 and ErrConflict on 409.
func (e *HTTPError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.StatusCode == http.StatusUnauthorized
	case ErrConflict:
		return e.StatusCode == http.StatusConflict
	}
	return false
}

// invalidUUIDError keeps the diagnostic of an identifier and matches
// ErrInvalidUUID.
type invalidUUIDError struct{ msg string }

func (e *invalidUUIDError) Error() string        { return e.msg }
func (e *invalidUUIDError) Is(target error) bool { return target == ErrInvalidUUID }
