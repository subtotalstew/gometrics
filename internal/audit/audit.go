// Package audit реализует аудит изменений метрик по шаблону
// «наблюдатель»: Subject хранит список приёмников, а каждый приёмник
// (файл, внешний HTTP-сервис) получает событие с именем метрики и IP клиента.
//
// Доставка событий выполняется в фоновом воркере: Subject.Notify только
// кладёт событие в буферизованную очередь и сразу возвращает управление,
// поэтому медленный или недоступный приёмник (например, внешний URL с
// таймаутом 5 секунд) не задерживает обработку HTTP-запроса.
package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	// defaultQueueSize — размер буфера очереди событий по умолчанию.
	defaultQueueSize = 256
	// closeTimeout — сколько ждём, пока воркер разберёт очередь при Close.
	closeTimeout = 5 * time.Second
)

// Event — событие аудита: какие метрики изменили и с какого IP.
type Event struct {
	// TS — время события в формате Unix.
	TS int64 `json:"ts"`
	// Metrics — имена изменённых метрик.
	Metrics []string `json:"metrics"`
	// IPAddress — IP клиента, отправившего изменение.
	IPAddress string `json:"ip_address"`
}

// Observer — приёмник событий аудита. Реализации: FileObserver, URLObserver.
type Observer interface {
	// Notify обрабатывает событие. Ошибка не останавливает остальные
	// приёмники — Subject её только логирует.
	Notify(event Event) error
}

// Subject — издатель событий аудита, который доставляет событие всем
// зарегистрированным Observer. Безопасен для конкурентного использования.
//
// По умолчанию доставка асинхронная: событие попадает в буферизованную
// очередь, которую разбирает одна фоновая горутина. Если очередь переполнена,
// событие отбрасывается (см. Dropped) — запрос метрики при этом не блокируется.
type Subject struct {
	// observers — неизменяемый список приёмников. Register публикует новый
	// список через CAS (copy-on-write), поэтому доставка читает его без
	// блокировок и без копирования на каждое событие.
	observers atomic.Pointer[[]Observer]

	queueSize int
	sync      bool
	jobs      chan Event
	stop      chan struct{}
	done      chan struct{}
	stopOnce  sync.Once

	dropped atomic.Int64
}

// SubjectOption настраивает Subject при создании.
type SubjectOption func(*Subject)

// WithQueueSize задаёт размер буфера очереди событий. Значение <= 0
// заменяется на размер по умолчанию.
func WithQueueSize(size int) SubjectOption {
	return func(s *Subject) {
		if size > 0 {
			s.queueSize = size
		}
	}
}

// WithSyncDelivery включает синхронную доставку: Subject.Notify вызывает
// приёмники в вызывающей горутине. Полезно в тестах и там, где события
// должны быть записаны строго до возврата из Notify.
func WithSyncDelivery() SubjectOption {
	return func(s *Subject) {
		s.sync = true
	}
}

// NewSubject создаёт издателя без приёмников. По умолчанию запускается
// фоновый воркер доставки; его нужно остановить методом Close.
func NewSubject(opts ...SubjectOption) *Subject {
	s := &Subject{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}

	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}

	if s.queueSize <= 0 {
		s.queueSize = defaultQueueSize
	}
	s.jobs = make(chan Event, s.queueSize)

	if s.sync {
		close(s.done)
		return s
	}

	go s.worker()
	return s
}

// Register добавляет приёмник в список. nil игнорируется.
//
// Список приёмников иммутабельный: Register собирает новый срез и атомарно
// подменяет старый, поэтому доставка событий читает список без блокировок
// и без аллокации копии на каждое событие.
func (s *Subject) Register(o Observer) {
	if o == nil {
		return
	}

	for {
		old := s.observers.Load()

		var prev []Observer
		if old != nil {
			prev = *old
		}

		next := make([]Observer, len(prev)+1)
		copy(next, prev)
		next[len(prev)] = o

		if s.observers.CompareAndSwap(old, &next) {
			return
		}
	}
}

// snapshot возвращает текущий список приёмников. Срез иммутабельный, поэтому
// его можно обходить без блокировок и без копирования.
func (s *Subject) snapshot() []Observer {
	if ptr := s.observers.Load(); ptr != nil {
		return *ptr
	}
	return nil
}

// Notify публикует событие. При асинхронной доставке (по умолчанию) событие
// кладётся в очередь и Notify возвращается сразу; при переполнении очереди
// событие отбрасывается с предупреждением в логе. При синхронной доставке
// (WithSyncDelivery) приёмники вызываются в текущей горутине.
//
// Имена метрик в событии должны принадлежать вызывающему коду и не
// изменяться после вызова; используйте WithNames, если срез может быть
// переиспользован.
func (s *Subject) Notify(event Event) {
	if s.sync {
		s.deliver(event)
		return
	}

	select {
	case s.jobs <- event:
	default:
		s.dropped.Add(1)
		log.Warn().
			Int("queue_size", cap(s.jobs)).
			Int64("dropped_total", s.dropped.Load()).
			Msg("очередь аудита переполнена, событие отброшено")
	}
}

// Dropped возвращает число событий, отброшенных из-за переполнения очереди.
func (s *Subject) Dropped() int64 {
	return s.dropped.Load()
}

// Close останавливает фоновый воркер, дожидаясь доставки уже поставленных
// в очередь событий, и закрывает приёмники, поддерживающие закрытие
// (например, FileObserver закрывает дескриптор файла). Повторный вызов
// безопасен. После Close новые события не доставляются: Notify отбрасывает
// их с предупреждением.
func (s *Subject) Close() error {
	if s.sync {
		return s.closeObservers()
	}

	s.stopOnce.Do(func() {
		close(s.stop)
	})

	select {
	case <-s.done:
	case <-time.After(closeTimeout):
		log.Warn().Msg("воркер аудита не успел доставить очередь за отведённое время")
	}

	return s.closeObservers()
}

// closeObservers закрывает приёмники, реализующие io.Closer.
func (s *Subject) closeObservers() error {
	var errs []error
	for _, o := range s.snapshot() {
		closer, ok := o.(io.Closer)
		if !ok {
			continue
		}
		if err := closer.Close(); err != nil {
			log.Error().Err(err).Msg("не удалось закрыть приёмник аудита")
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// worker последовательно разбирает очередь и доставляет события приёмникам.
// При получении сигнала остановки он доразбирает уже накопленные события,
// чтобы они не потерялись при graceful shutdown.
func (s *Subject) worker() {
	defer close(s.done)

	for {
		select {
		case event := <-s.jobs:
			s.deliver(event)
		case <-s.stop:
			s.drain()
			return
		}
	}
}

// drain доставляет события, оставшиеся в очереди к моменту остановки.
func (s *Subject) drain() {
	for {
		select {
		case event := <-s.jobs:
			s.deliver(event)
		default:
			return
		}
	}
}

// deliver вызывает все приёмники по очереди. Список приёмников берётся
// из атомарного снапшота, поэтому копирование на каждое событие не нужно.
// Ошибки логируются и не прерывают рассылку; паника одного приёмника не
// должна уронить воркер и потерять остальные события.
func (s *Subject) deliver(event Event) {
	for _, o := range s.snapshot() {
		if err := notify(o, event); err != nil {
			log.Error().Err(err).Msg("ошибка отправки события аудита")
		}
	}
}

// notify изолирует вызов приёмника: паника в приёмнике превращается в ошибку
// и не приводит к падению воркера доставки.
func notify(o Observer, event Event) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Error().Interface("panic", rec).Msg("паника в приёмнике аудита")
			err = nil
		}
	}()

	return o.Notify(event)
}

// NewEvent создаёт событие с текущим временем, списком метрик и IP клиента.
func NewEvent(metrics []string, ip string) Event {
	return Event{
		TS:        time.Now().Unix(),
		Metrics:   metrics,
		IPAddress: ip,
	}
}

// WithNames возвращает копию события с собственной копией списка метрик.
// Асинхронная доставка откладывает обработку события, поэтому срез,
// разделяемый с запросом, нужно копировать при постановке в очередь.
func WithNames(event Event) Event {
	names := make([]string, len(event.Metrics))
	copy(names, event.Metrics)
	event.Metrics = names
	return event
}

// FileObserver записывает события аудита в файл: по одному JSON-объекту
// на строку (JSON Lines).
//
// Дескриптор файла открывается при первой записи и переиспользуется
// всеми последующими событиями — открывать файл на каждое событие дорого.
type FileObserver struct {
	path string
	mu   sync.Mutex
	file *os.File
}

// NewFileObserver создаёт приёмник, пишущий в файл path. Файл создаётся
// при первой записи, если его нет; дескриптор закрывается методом Close.
func NewFileObserver(path string) *FileObserver {
	return &FileObserver{path: path}
}

// Notify дописывает событие в конец файла. Записи сериализованы мьютексом,
// поэтому событие не может «разорваться» между двумя горутинами.
func (f *FileObserver) Notify(event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.file == nil {
		file, err := os.OpenFile(f.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		f.file = file
	}

	_, err = f.file.Write(data)
	return err
}

// Close закрывает дескриптор файла. Вызов безопасен, если файл так и не был
// открыт, и повторный вызов после закрытия не считается ошибкой.
func (f *FileObserver) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.file == nil {
		return nil
	}

	err := f.file.Close()
	f.file = nil
	return err
}

// isOpen сообщает, держит ли приёмник открытый дескриптор файла.
func (f *FileObserver) isOpen() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.file != nil
}

// URLObserver отправляет события аудита POST-запросом на внешний сервис.
type URLObserver struct {
	url    string
	client *http.Client
}

// NewURLObserver создаёт приёмник, отправляющий события на url.
// HTTP-клиент имеет таймаут 5 секунд.
func NewURLObserver(url string) *URLObserver {
	return &URLObserver{
		url:    url,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Notify отправляет событие как JSON с Content-Type application/json.
// Ошибки транспорта возвращаются вызывающему, а ответы сервера аудита
// с кодом 5xx только логируются: событие считается доставленным.
func (u *URLObserver) Notify(event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, u.url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusInternalServerError {
		log.Warn().Int("status", resp.StatusCode).Str("url", u.url).Msg("сервер аудита вернул ошибку")
	}
	return nil
}
