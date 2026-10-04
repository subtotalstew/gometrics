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

func (m *mockObserver) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

// newSyncSubject создаёт издателя с синхронной доставкой: тесты, которые
// проверяют поведение приёмников, не зависят от планировщика горутин.
func newSyncSubject() *Subject {
	return NewSubject(WithSyncDelivery())
}

func TestSubject_NotifyNoObservers(t *testing.T) {
	s := newSyncSubject()
	// Не должно паниковать при отсутствии наблюдателей.
	s.Notify(NewEvent([]string{"Alloc"}, "127.0.0.1"))
}

func TestSubject_NotifySingleObserver(t *testing.T) {
	s := newSyncSubject()
	obs := &mockObserver{}
	s.Register(obs)

	s.Notify(Event{TS: 123, Metrics: []string{"Alloc"}, IPAddress: "10.0.0.1"})

	got := obs.Events()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].TS != 123 {
		t.Errorf("TS = %d, want 123", got[0].TS)
	}
	if len(got[0].Metrics) != 1 || got[0].Metrics[0] != "Alloc" {
		t.Errorf("Metrics = %v, want [Alloc]", got[0].Metrics)
	}
	if got[0].IPAddress != "10.0.0.1" {
		t.Errorf("IPAddress = %q, want 10.0.0.1", got[0].IPAddress)
	}
}

func TestSubject_NotifyMultipleObservers(t *testing.T) {
	s := newSyncSubject()
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
	s := newSyncSubject()
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
	s := newSyncSubject()
	// nil-наблюдатель не должен добавляться и не должен вызывать панику.
	s.Register(nil)
	s.Notify(NewEvent([]string{"Alloc"}, "127.0.0.1"))
}

func TestSubject_RegisterConcurrently(t *testing.T) {
	s := newSyncSubject()
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

// ---- асинхронная доставка ----

// channelObserver отдаёт события в канал, чтобы тест мог дождаться доставки.
type channelObserver struct {
	events chan Event
	err    error
}

func (c *channelObserver) Notify(e Event) error {
	c.events <- e
	return c.err
}

// waitForEvents ждёт n событий или падает по таймауту.
func waitForEvents(t *testing.T, ch <-chan Event, n int) []Event {
	t.Helper()

	out := make([]Event, 0, n)
	deadline := time.After(2 * time.Second)
	for len(out) < n {
		select {
		case e := <-ch:
			out = append(out, e)
		case <-deadline:
			t.Fatalf("получено %d событий из %d за 2s", len(out), n)
		}
	}
	return out
}

func TestSubject_NotifyAsyncDeliversInBackground(t *testing.T) {
	s := NewSubject()
	defer s.Close()

	obs := &channelObserver{events: make(chan Event, 1)}
	s.Register(obs)

	s.Notify(Event{TS: 7, Metrics: []string{"Alloc"}, IPAddress: "10.0.0.1"})

	got := waitForEvents(t, obs.events, 1)
	if got[0].TS != 7 || got[0].IPAddress != "10.0.0.1" {
		t.Errorf("событие доставлено неверно: %+v", got[0])
	}
}

// blockingObserver имитирует недоступный внешний приёмник: Notify висит,
// пока тест не разрешит продолжить.
type blockingObserver struct {
	release chan struct{}
	started chan struct{}
}

func (b *blockingObserver) Notify(Event) error {
	// Отметку о старте отправляем без блокировки: тест может её и не ждать,
	// а приёмник должен «висеть» только на release.
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-b.release
	return nil
}

func TestSubject_NotifyDoesNotBlockRequestOnSlowObserver(t *testing.T) {
	s := NewSubject()
	defer s.Close()

	slow := &blockingObserver{
		release: make(chan struct{}),
		started: make(chan struct{}, 1),
	}
	s.Register(slow)

	const n = 3
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			s.Notify(Event{TS: int64(i), Metrics: []string{"Alloc"}, IPAddress: "127.0.0.1"})
		}
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Notify заблокировался на медленном приёмнике")
	}

	// Воркер уже занят первым событием, остальные ждут в очереди.
	<-slow.started
	close(slow.release)
}

func TestSubject_DropsWhenQueueFull(t *testing.T) {
	s := NewSubject(WithQueueSize(1))
	defer s.Close()

	slow := &blockingObserver{
		release: make(chan struct{}),
		started: make(chan struct{}, 1),
	}
	s.Register(slow)

	// Первое событие воркер забирает и зависает на нём.
	s.Notify(Event{TS: 0, Metrics: []string{"Alloc"}, IPAddress: "127.0.0.1"})
	<-slow.started

	// Второе событие занимает очередь целиком, третье и четвёртое должны
	// быть отброшены без блокировки.
	for i := 1; i <= 3; i++ {
		s.Notify(Event{TS: int64(i), Metrics: []string{"Alloc"}, IPAddress: "127.0.0.1"})
	}

	if dropped := s.Dropped(); dropped == 0 {
		t.Error("ожидали отброшенные события при переполнении очереди, получили 0")
	}

	close(slow.release)
}

func TestSubject_CloseDrainsQueue(t *testing.T) {
	s := NewSubject(WithQueueSize(16))

	obs := &mockObserver{}
	s.Register(obs)

	const n = 5
	for i := 0; i < n; i++ {
		s.Notify(Event{TS: int64(i), Metrics: []string{"Alloc"}, IPAddress: "127.0.0.1"})
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// После Close очередь должна быть разобрана целиком.
	if got := obs.Len(); got != n {
		t.Errorf("после Close доставлено %d событий, want %d", got, n)
	}
}

func TestSubject_CloseIdempotent(t *testing.T) {
	s := NewSubject()
	if err := s.Close(); err != nil {
		t.Fatalf("первый Close() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("повторный Close() error = %v", err)
	}
}

func TestSubject_CloseClosesObservers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	fileObs := NewFileObserver(path)
	s := NewSubject()
	s.Register(fileObs)

	s.Notify(Event{TS: 1, Metrics: []string{"A"}, IPAddress: "1.1.1.1"})
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if fileObs.isOpen() {
		t.Error("дескриптор файла приёмника не закрыт после Subject.Close")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), `"ts":1`) {
		t.Errorf("событие не записано: %q", data)
	}
}

func TestWithNames_CopiesSlice(t *testing.T) {
	names := []string{"Alloc"}
	event := WithNames(Event{TS: 1, Metrics: names, IPAddress: "1.1.1.1"})

	names[0] = "Changed"

	if event.Metrics[0] != "Alloc" {
		t.Errorf("Metrics = %v, want [Alloc]: срез должен быть скопирован", event.Metrics)
	}
}

func TestNewEvent(t *testing.T) {
	before := time.Now().Unix()
	e := NewEvent([]string{"Alloc", "Frees"}, "10.0.0.1")
	after := time.Now().Unix()

	if e.TS < before || e.TS > after {
		t.Errorf("Ts = %d, want between %d and %d", e.TS, before, after)
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
	defer obs.Close()
	event := Event{TS: 12345678, Metrics: []string{"Alloc", "Frees"}, IPAddress: "192.168.0.42"}

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
	if got.TS != 12345678 {
		t.Errorf("Ts = %d, want 12345678", got.TS)
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
	defer obs.Close()

	if err := obs.Notify(Event{TS: 1, Metrics: []string{"A"}, IPAddress: "1.1.1.1"}); err != nil {
		t.Fatalf("Notify() #1 error = %v", err)
	}
	if err := obs.Notify(Event{TS: 2, Metrics: []string{"B"}, IPAddress: "2.2.2.2"}); err != nil {
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
	if first.TS != 1 || second.TS != 2 {
		t.Errorf("order wrong: first.TS = %d, second.TS = %d", first.TS, second.TS)
	}
}

func TestFileObserver_NotifyCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "audit.log")
	// Директории не существует — ожидаем ошибку.
	obs := NewFileObserver(path)

	err := obs.Notify(Event{TS: 1, Metrics: []string{"A"}, IPAddress: "1.1.1.1"})
	if err == nil {
		// Если бы os.OpenFile создавал директории — тест бы упал.
		// Но os.OpenFile не создаёт промежуточные директории, поэтому err != nil.
		t.Fatal("expected error for missing parent directory, got nil")
	}
}

func TestFileObserver_KeepsHandleOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	obs := NewFileObserver(path)
	defer obs.Close()

	if obs.isOpen() {
		t.Fatal("дескриптор открыт до первой записи")
	}

	if err := obs.Notify(Event{TS: 1, Metrics: []string{"A"}, IPAddress: "1.1.1.1"}); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if !obs.isOpen() {
		t.Fatal("после записи дескриптор должен оставаться открытым")
	}

	// Все последующие события пишутся в тот же дескриптор.
	for i := 2; i <= 4; i++ {
		if err := obs.Notify(Event{TS: int64(i), Metrics: []string{"A"}, IPAddress: "1.1.1.1"}); err != nil {
			t.Fatalf("Notify(%d) error = %v", i, err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if lines := strings.Count(string(data), "\n"); lines != 4 {
		t.Errorf("записано %d строк, want 4", lines)
	}
}

func TestFileObserver_CloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	obs := NewFileObserver(path)
	if err := obs.Close(); err != nil {
		t.Fatalf("Close() без открытия файла error = %v", err)
	}

	if err := obs.Notify(Event{TS: 1, Metrics: []string{"A"}, IPAddress: "1.1.1.1"}); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if err := obs.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// После закрытия приёмник снова откроет файл при следующей записи.
	if err := obs.Notify(Event{TS: 2, Metrics: []string{"B"}, IPAddress: "2.2.2.2"}); err != nil {
		t.Fatalf("Notify() после Close error = %v", err)
	}
	if err := obs.Close(); err != nil {
		t.Fatalf("повторный Close() error = %v", err)
	}
}

func TestFileObserver_NotifyConcurrently(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	obs := NewFileObserver(path)
	defer obs.Close()

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = obs.Notify(Event{TS: int64(i), Metrics: []string{"A"}, IPAddress: "1.1.1.1"})
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
