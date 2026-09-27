package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- Subject ----

type mockObserver struct {
	mu     sync.Mutex
	events []Event
	err    error
}

func (m *mockObserver) Notify(e Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
	return m.err
}

func (m *mockObserver) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

func TestSubject_NotifyNoObservers(t *testing.T) {
	s := NewSubject()
	// Не должно паниковать при отсутствии наблюдателей.
	s.Notify(NewEvent([]string{"Alloc"}, "127.0.0.1"))
}

func TestSubject_NotifySingleObserver(t *testing.T) {
	s := NewSubject()
	obs := &mockObserver{}
	s.Register(obs)

	s.Notify(Event{Ts: 123, Metrics: []string{"Alloc"}, IPAddress: "10.0.0.1"})

	got := obs.Events()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].Ts != 123 {
		t.Errorf("Ts = %d, want 123", got[0].Ts)
	}
	if len(got[0].Metrics) != 1 || got[0].Metrics[0] != "Alloc" {
		t.Errorf("Metrics = %v, want [Alloc]", got[0].Metrics)
	}
	if got[0].IPAddress != "10.0.0.1" {
		t.Errorf("IPAddress = %q, want 10.0.0.1", got[0].IPAddress)
	}
}

func TestSubject_NotifyMultipleObservers(t *testing.T) {
	s := NewSubject()
	obs1 := &mockObserver{}
	obs2 := &mockObserver{}
	s.Register(obs1)
	s.Register(obs2)

	s.Notify(NewEvent([]string{"Alloc", "Frees"}, "192.168.0.42"))

	if len(obs1.Events()) != 1 {
		t.Errorf("obs1 got %d events, want 1", len(obs1.Events()))
	}
	if len(obs2.Events()) != 1 {
		t.Errorf("obs2 got %d events, want 1", len(obs2.Events()))
	}
}

func TestSubject_NotifyContinuesOnObserverError(t *testing.T) {
	s := NewSubject()
	bad := &mockObserver{err: os.ErrPermission}
	good := &mockObserver{}
	s.Register(bad)
	s.Register(good)

	s.Notify(NewEvent([]string{"Alloc"}, "127.0.0.1"))

	// Хороший наблюдатель должен получить событие, несмотря на ошибку плохого.
	if len(good.Events()) != 1 {
		t.Errorf("good observer got %d events, want 1", len(good.Events()))
	}
}

func TestSubject_RegisterNil(t *testing.T) {
	s := NewSubject()
	// nil-наблюдатель не должен добавляться и не должен вызывать панику.
	s.Register(nil)
	s.Notify(NewEvent([]string{"Alloc"}, "127.0.0.1"))
}

func TestSubject_RegisterConcurrently(t *testing.T) {
	s := NewSubject()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Register(&mockObserver{})
		}()
	}
	wg.Wait()

	obs := &mockObserver{}
	s.Register(obs)
	s.Notify(NewEvent([]string{"Alloc"}, "127.0.0.1"))

	if len(obs.Events()) != 1 {
		t.Errorf("observer after concurrent Register got %d events, want 1", len(obs.Events()))
	}
}

func TestNewEvent(t *testing.T) {
	before := time.Now().Unix()
	e := NewEvent([]string{"Alloc", "Frees"}, "10.0.0.1")
	after := time.Now().Unix()

	if e.Ts < before || e.Ts > after {
		t.Errorf("Ts = %d, want between %d and %d", e.Ts, before, after)
	}
	if len(e.Metrics) != 2 {
		t.Errorf("Metrics len = %d, want 2", len(e.Metrics))
	}
	if e.IPAddress != "10.0.0.1" {
		t.Errorf("IPAddress = %q, want 10.0.0.1", e.IPAddress)
	}
}

// ---- FileObserver ----

func TestFileObserver_NotifyWritesLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	obs := NewFileObserver(path)
	event := Event{Ts: 12345678, Metrics: []string{"Alloc", "Frees"}, IPAddress: "192.168.0.42"}

	if err := obs.Notify(event); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	line := strings.TrimRight(string(data), "\n")
	if strings.Contains(line, "\n") {
		t.Errorf("expected single line, got %q", data)
	}

	var got Event
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v, body = %q", err, line)
	}
	if got.Ts != 12345678 {
		t.Errorf("Ts = %d, want 12345678", got.Ts)
	}
	if len(got.Metrics) != 2 || got.Metrics[0] != "Alloc" || got.Metrics[1] != "Frees" {
		t.Errorf("Metrics = %v, want [Alloc Frees]", got.Metrics)
	}
	if got.IPAddress != "192.168.0.42" {
		t.Errorf("IPAddress = %q, want 192.168.0.42", got.IPAddress)
	}
}

func TestFileObserver_NotifyAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	obs := NewFileObserver(path)

	if err := obs.Notify(Event{Ts: 1, Metrics: []string{"A"}, IPAddress: "1.1.1.1"}); err != nil {
		t.Fatalf("Notify() #1 error = %v", err)
	}
	if err := obs.Notify(Event{Ts: 2, Metrics: []string{"B"}, IPAddress: "2.2.2.2"}); err != nil {
		t.Fatalf("Notify() #2 error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}

	var first, second Event
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("Unmarshal() line 1 error = %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("Unmarshal() line 2 error = %v", err)
	}
	if first.Ts != 1 || second.Ts != 2 {
		t.Errorf("order wrong: first.Ts = %d, second.Ts = %d", first.Ts, second.Ts)
	}
}

func TestFileObserver_NotifyCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "audit.log")
	// Директории не существует — ожидаем ошибку.
	obs := NewFileObserver(path)

	err := obs.Notify(Event{Ts: 1, Metrics: []string{"A"}, IPAddress: "1.1.1.1"})
	if err == nil {
		// Если бы os.OpenFile создавал директории — тест бы упал.
		// Но os.OpenFile не создаёт промежуточные директории, поэтому err != nil.
		t.Fatal("expected error for missing parent directory, got nil")
	}
}

func TestFileObserver_NotifyConcurrently(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	obs := NewFileObserver(path)

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = obs.Notify(Event{Ts: int64(i), Metrics: []string{"A"}, IPAddress: "1.1.1.1"})
		}(i)
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != n {
		t.Errorf("got %d lines, want %d", len(lines), n)
	}

	// Каждая строка должна быть валидным JSON.
	for i, line := range lines {
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Errorf("line %d not valid JSON: %v, body = %q", i, err, line)
		}
	}
}
