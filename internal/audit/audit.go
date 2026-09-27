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

type Event struct {
	Ts        int64    `json:"ts"`
	Metrics   []string `json:"metrics"`
	IPAddress string   `json:"ip_address"`
}

// Observer
type Observer interface {
	Notify(event Event) error
}

// Subject
type Subject struct {
	mu        sync.RWMutex
	observers []Observer
}

func NewSubject() *Subject {
	return &Subject{}
}

func (s *Subject) Register(o Observer) {
	if o == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observers = append(s.observers, o)
}

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

func NewEvent(metrics []string, ip string) Event {
	return Event{
		Ts:        time.Now().Unix(),
		Metrics:   metrics,
		IPAddress: ip,
	}
}

type FileObserver struct {
	path string
	mu   sync.Mutex
}

func NewFileObserver(path string) *FileObserver {
	return &FileObserver{path: path}
}

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

type URLObserver struct {
	url    string
	client *http.Client
}

func NewURLObserver(url string) *URLObserver {
	return &URLObserver{
		url:    url,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

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
