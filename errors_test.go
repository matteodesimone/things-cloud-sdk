package thingscloud

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// preflightServer serves one raw history page for the Task7 preflight and
// fails the test on any POST: every case below must stop before writing.
func preflightServer(t *testing.T, page string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected %s request: the preflight must refuse before posting", r.Method)
			return
		}
		fmt.Fprint(w, page)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestHistoryWritePreflightErrorsAreTyped(t *testing.T) {
	t.Parallel()

	ordinary := `{"tt":"x","rr":null,"rp":null,"rt":[]}`
	missing, area, recurring, sparse := NewUUID(), NewUUID(), NewUUID(), NewUUID()
	page := historyPage(4,
		fmt.Sprintf(`{%q:{"e":"Area3","t":0,"p":{"tt":"An area","ix":0,"tg":[],"xx":{"sn":{},"_t":"oo"}}}}`, area),
		taskHistoryBatch(recurring, ItemKindTask7, 0, `{"tt":"x","rr":{"fu":16,"fa":1,"of":[{"dy":0}],"rrv":4},"rp":null,"rt":[]}`),
		taskHistoryBatch(sparse, ItemKindTask7, 0, `{"tt":"x"}`),
		taskHistoryBatch(NewUUID(), ItemKindTask7, 0, ordinary),
	)
	cases := []struct {
		name   string
		target string
		want   error
	}{
		{"missing", missing, ErrNotFound},
		{"area", area, ErrWrongKind},
		{"recurring", recurring, ErrRecurring},
		{"without recurrence markers", sparse, ErrUnverifiedTarget},
	}
	for _, c := range cases {
		server := preflightServer(t, page)
		h := New(server.URL, "test@example.com", "password").HistoryWithID("history")
		err := h.Write(rawWriteProbe{id: c.target, body: task7UpdateBody(`"tt":"new"`)})
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		var pe *PreflightError
		if !errors.As(err, &pe) || pe.Target != c.target {
			t.Errorf("%s: err = %#v, want a PreflightError on the target", c.name, err)
		}
	}
}

func TestHistoryWriteInvalidUUIDIsTyped(t *testing.T) {
	t.Parallel()

	h := New("http://127.0.0.1:1", "test@example.com", "password").HistoryWithID("history")
	err := h.Write(rawWriteProbe{id: "not-a-uuid", body: task7CreateBody("x")})
	if !errors.Is(err, ErrInvalidUUID) {
		t.Fatalf("err = %v, want ErrInvalidUUID", err)
	}
	if err := ValidateUUID(""); !errors.Is(err, ErrInvalidUUID) {
		t.Fatalf("ValidateUUID(\"\") = %v, want ErrInvalidUUID", err)
	}
}

func TestHistoryWriteConflictIsTyped(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "stale ancestor", http.StatusConflict)
	}))
	defer server.Close()
	h := New(server.URL, "test@example.com", "password").HistoryWithID("history")
	err := h.Write(rawWriteProbe{id: NewUUID(), body: task7CreateBody("x")})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != http.StatusConflict {
		t.Fatalf("err = %#v, want an HTTPError with 409", err)
	}
}

func TestVerifyErrorsAreTyped(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusInternalServerError, nil},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
		}))
		_, err := New(server.URL, "test@example.com", "password").Verify()
		server.Close()
		var he *HTTPError
		switch {
		case c.want != nil && !errors.Is(err, c.want):
			t.Errorf("%d: err = %v, want %v", c.status, err, c.want)
		case c.want == nil && (!errors.As(err, &he) || he.StatusCode != c.status):
			t.Errorf("%d: err = %#v, want an HTTPError", c.status, err)
		}
	}
}
