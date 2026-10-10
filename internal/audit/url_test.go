package audit

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestURLObserver_Notify_Success(t *testing.T) {
	t.Parallel()

	var got Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	obs := NewURLObserver(srv.URL)
	event := Event{TS: 42, Metrics: []string{"Alloc"}, IPAddress: "10.0.0.1"}
	if err := obs.Notify(event); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if got.TS != 42 || got.IPAddress != "10.0.0.1" {
		t.Errorf("server received %+v, want %+v", got, event)
	}
}

func TestURLObserver_Notify_ServerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	obs := NewURLObserver(srv.URL)
	if err := obs.Notify(Event{TS: 1, Metrics: []string{"A"}}); err != nil {
		// По контракту Notify 5xx не возвращает ошибку — только логирует.
		t.Fatalf("Notify() error = %v, want nil for 5xx", err)
	}
}

func TestURLObserver_Notify_BadURL(t *testing.T) {
	t.Parallel()

	obs := NewURLObserver("http://127.0.0.1:0")
	if err := obs.Notify(Event{TS: 1, Metrics: []string{"A"}}); err == nil {
		t.Fatal("Notify() = nil, want error for unreachable server")
	}
}
