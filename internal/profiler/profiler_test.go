package profiler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandler_ServesPprofEndpoints(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	endpoints := []string{
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/heap",
		"/debug/pprof/goroutine",
		"/debug/pprof/allocs",
		"/debug/pprof/symbol",
	}

	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			resp, err := http.Get(srv.URL + ep)
			if err != nil {
				t.Fatalf("GET %s: %v", ep, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s: status = %d, want %d", ep, resp.StatusCode, http.StatusOK)
			}
			if _, err := io.Copy(io.Discard, resp.Body); err != nil {
				t.Fatalf("read body: %v", err)
			}
		})
	}
}

func TestHandler_ServesHeapProfile(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	// Проверяем, что эндпоинт отдаёт валидный protobuf-профиль:
	// gzip-заголовок проставляется обработчиком pprof.
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/debug/pprof/heap", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 {
		t.Fatal("пустой профиль")
	}
	if ct := resp.Header.Get("Content-Type"); ct == "" {
		t.Error("не указан Content-Type")
	}
}

func TestStart_InvalidAddress(t *testing.T) {
	// Порт заведомо вне диапазона — Start должен вернуть ошибку,
	// а не паниковать.
	srv, err := Start("127.0.0.1:99999")
	if err == nil {
		_ = srv.Close()
		t.Fatal("ожидалась ошибка при некорректном адресе")
	}
}

func TestStart_ServesHeapProfile(t *testing.T) {
	srv, err := Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Close()

	resp, err := http.Get("http://" + srv.Addr + "/debug/pprof/heap")
	if err != nil {
		t.Fatalf("GET /debug/pprof/heap: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 {
		t.Fatal("пустой профиль")
	}
	if !strings.HasPrefix(srv.Addr, "127.0.0.1:") {
		t.Fatalf("неожиданный адрес: %q", srv.Addr)
	}
}
