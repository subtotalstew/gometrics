// Package audit реализует аудит изменений метрик по шаблону
// «наблюдатель»: Subject хранит список приёмников, а каждый приёмник
// (файл, внешний HTTP-сервис) получает событие с именем метрики и IP клиента.
package audit

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// Event — событие аудита: какие метрики изменили и с какого IP.
type Event struct {
	// Ts — время события в формате Unix.
	Ts int64 `json:"ts"`
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

// Subject — издатель событий аудита, который рассылает событие всем
// зарегистрированным Observer. Безопасен для конкурентного использования.
type Subject struct {
	mu        sync.RWMutex
	observers []Observer
}

// NewSubject создаёт издателя без приёмников.
func NewSubject() *Subject {
	return &Subject{}
}

// Register добавляет приёмник в список. nil игнорируется.
func (s *Subject) Register(o Observer) {
	if o == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observers = append(s.observers, o)
}

// Notify рассылает событие всем зарегистрированным приёмникам.
// Ошибки приёмников логируются и не прерывают рассылку.
func (s *Subject) Notify(event Event) {
	s.mu.RLock()
	observers := make([]Observer, len(s.observers))
	copy(observers, s.observers)
	s.mu.RUnlock()

	for _, o := range observers {
		if err := o.Notify(event); err != nil {
			log.Error().Err(err).Msg("ошибка отправки события аудита")
		}
	}
}

// NewEvent создаёт событие с текущим временем, списком метрик и IP клиента.
func NewEvent(metrics []string, ip string) Event {
	return Event{
		Ts:        time.Now().Unix(),
		Metrics:   metrics,
		IPAddress: ip,
	}
}

// FileObserver записывает события аудита в файл: по одному JSON-объекту
// на строку (JSON Lines).
type FileObserver struct {
	path string
	mu   sync.Mutex
}

// NewFileObserver создаёт приёмник, пишущий в файл path. Файл создаётся
// при первой записи, если его нет.
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

	file, err := os.OpenFile(f.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(data)
	return err
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
