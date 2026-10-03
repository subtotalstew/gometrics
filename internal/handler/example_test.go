package handler_test

// Этот файл содержит исполняемые примеры работы с эндпоинтами сервиса
// метрик («практический трек»). Примеры запускаются вместе с тестами
// (`go test ./internal/handler`) и одновременно служат документацией:
// ожидаемые ответы проверяются директивой `// Output:`.
//
// Запустить только примеры:
//
//	go test ./internal/handler -run Example -v

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/subtotalstew/gometrics.git/internal/audit"
	"github.com/subtotalstew/gometrics.git/internal/handler"
	"github.com/subtotalstew/gometrics.git/internal/hash"
	models "github.com/subtotalstew/gometrics.git/internal/model"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

// newServer собирает сервис метрик так же, как это делает cmd/server:
// middleware gzip + логирование, эндпоинты практического трека поверх
// хранилища в памяти. Возвращается httptest-сервер с готовым базовым адресом.
func newServer() *httptest.Server {
	h := handler.NewHandler(storage.NewMemStorage())

	r := chi.NewRouter()
	r.Use(h.GzipMiddleware)
	r.Use(h.LoggingMiddleware)
	r.Use(h.HashMiddleware)

	r.Post("/update", h.UpdateJSONHandler)
	r.Post("/value", h.ValueJSONHandler)
	r.Post("/updates/", h.UpdatesJSONHandler)
	r.Post("/update/{type}/{name}/{value}", h.UpdateHandler)
	r.Get("/value/{type}/{name}", h.ValueHandler)
	r.Get("/", h.RootHandler)

	return httptest.NewServer(r)
}

// do выполняет запрос к серверу примера. Ошибки транспортного уровня
// паникуют: в примере они означают ошибку самого примера, а не проверяемое
// поведение сервиса.
//
// Заголовок Accept-Encoding: identity отключает автоматическое
// разжатие gzip в http.Transport, чтобы примеры читали тело ответа «как есть».
//
// Тело ответа закрывает вызывающий код — closeBody или readBody.
func do(req *http.Request) *http.Response {
	if req.Header.Get("Accept-Encoding") == "" {
		req.Header.Set("Accept-Encoding", "identity")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	return resp
}

// closeBody закрывает тело ответа, когда содержимое не нужно.
func closeBody(resp *http.Response) {
	if err := resp.Body.Close(); err != nil {
		panic(err)
	}
}

// readBody вычитывает тело ответа целиком, чтобы соединение вернулось
// в пул HTTP-клиента, и закрывает его.
func readBody(resp *http.Response) string {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	closeBody(resp)
	return string(body)
}

// readGzipBody разжимает тело сжатого ответа сервиса (Content-Encoding: gzip),
// читает его целиком и закрывает ответ.
func readGzipBody(resp *http.Response) string {
	zr := must(gzip.NewReader(resp.Body))
	defer zr.Close()

	body, err := io.ReadAll(zr)
	if err != nil {
		panic(err)
	}
	closeBody(resp)
	return string(body)
}

// Example: сохранение метрики «по пути».
//
// POST /update/{type}/{name}/{value} — самый простой способ записать метрику:
// тип, имя и значение передаются прямо в URL. Для gauge значение
// перезаписывается, для counter — прибавляется к накопленному.
func Example_practicalTrack_updatePath() {
	srv := newServer()
	defer srv.Close()

	// gauge: записать текущую температуру.
	resp := do(must(http.NewRequest(http.MethodPost, srv.URL+"/update/gauge/temperature/42.5", nil)))
	fmt.Println("gauge:", resp.StatusCode)
	closeBody(resp)

	// counter: увеличить счётчик запросов на 7.
	resp = do(must(http.NewRequest(http.MethodPost, srv.URL+"/update/counter/requests_total/7", nil)))
	fmt.Println("counter:", resp.StatusCode)
	closeBody(resp)

	// Output:
	// gauge: 200
	// counter: 200
}

// Example: обновление метрики через JSON.
//
// POST /update принимает models.Metrics. Для gauge заполняется value,
// для counter — delta; в ответе для counter возвращается уже накопленное
// значение счётчика.
func Example_practicalTrack_updateJSON() {
	srv := newServer()
	defer srv.Close()

	value := 36.6
	body, _ := json.Marshal(models.Metrics{ID: "temperature", MType: models.Gauge, Value: &value})

	req := must(http.NewRequest(http.MethodPost, srv.URL+"/update", bytes.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")

	resp := do(req)
	fmt.Println("status:", resp.StatusCode)
	fmt.Println("body:", readBody(resp))

	// Output:
	// status: 200
	// body: {"id":"temperature","type":"gauge","value":36.6}
}

// Example: чтение метрики «по пути».
//
// GET /value/{type}/{name} возвращает значение в текстовом виде:
// gauge — в формате %g, counter — десятичным целым.
func Example_practicalTrack_valuePath() {
	srv := newServer()
	defer srv.Close()

	resp := do(must(http.NewRequest(http.MethodPost, srv.URL+"/update/gauge/temperature/42.5", nil)))
	closeBody(resp)
	resp = do(must(http.NewRequest(http.MethodPost, srv.URL+"/update/counter/requests_total/7", nil)))
	closeBody(resp)

	resp = do(must(http.NewRequest(http.MethodGet, srv.URL+"/value/gauge/temperature", nil)))
	fmt.Println("gauge:", readBody(resp))

	resp = do(must(http.NewRequest(http.MethodGet, srv.URL+"/value/counter/requests_total", nil)))
	fmt.Println("counter:", readBody(resp))

	// Output:
	// gauge: 42.5
	// counter: 7
}

// Example: чтение метрики через JSON.
//
// POST /value принимает метрику с id и type и возвращает её актуальное
// значение. Незнакомая метрика — это 404.
func Example_practicalTrack_valueJSON() {
	srv := newServer()
	defer srv.Close()

	resp := do(must(http.NewRequest(http.MethodPost, srv.URL+"/update/gauge/temperature/42.5", nil)))
	closeBody(resp)

	query, _ := json.Marshal(models.Metrics{ID: "temperature", MType: models.Gauge})
	req := must(http.NewRequest(http.MethodPost, srv.URL+"/value", bytes.NewReader(query)))
	req.Header.Set("Content-Type", "application/json")

	resp = do(req)
	fmt.Println("status:", resp.StatusCode)
	fmt.Println("body:", strings.TrimRight(readBody(resp), "\n"))

	query, _ = json.Marshal(models.Metrics{ID: "unknown", MType: models.Gauge})
	req = must(http.NewRequest(http.MethodPost, srv.URL+"/value", bytes.NewReader(query)))
	req.Header.Set("Content-Type", "application/json")

	resp = do(req)
	fmt.Println("unknown:", resp.StatusCode)
	closeBody(resp)

	// Output:
	// status: 200
	// body: {"id":"temperature","type":"gauge","value":42.5}
	// unknown: 404
}

// Example: батч-обновление метрик.
//
// POST /updates/ принимает JSON-массив метрик. Батч валидируется целиком,
// поэтому одна некорректная метрика отменяет запись всего батча.
func Example_practicalTrack_updatesBatch() {
	srv := newServer()
	defer srv.Close()

	alloc := 1024.0
	pollCount := int64(1)
	batch := []models.Metrics{
		{ID: "Alloc", MType: models.Gauge, Value: &alloc},
		{ID: "PollCount", MType: models.Counter, Delta: &pollCount},
	}
	body, _ := json.Marshal(batch)

	req := must(http.NewRequest(http.MethodPost, srv.URL+"/updates/", bytes.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	resp := do(req)
	fmt.Println("batch:", resp.StatusCode)
	closeBody(resp)

	resp = do(must(http.NewRequest(http.MethodGet, srv.URL+"/value/gauge/Alloc", nil)))
	fmt.Println("Alloc:", readBody(resp))

	resp = do(must(http.NewRequest(http.MethodGet, srv.URL+"/value/counter/PollCount", nil)))
	fmt.Println("PollCount:", readBody(resp))

	// Output:
	// batch: 200
	// Alloc: 1024
	// PollCount: 1
}

// Example: батч с gzip.
//
// Тело запроса можно сжать: достаточно передать Content-Encoding: gzip.
// Ответ на application/json сервис тоже сжимает при Accept-Encoding: gzip.
func Example_practicalTrack_updatesBatchGzip() {
	srv := newServer()
	defer srv.Close()

	alloc := 2048.0
	body, _ := json.Marshal([]models.Metrics{{ID: "Alloc", MType: models.Gauge, Value: &alloc}})

	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	gz.Write(body)
	gz.Close()

	req := must(http.NewRequest(http.MethodPost, srv.URL+"/updates/", bytes.NewReader(compressed.Bytes())))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	resp := do(req)
	fmt.Println("compressed request:", resp.StatusCode)
	closeBody(resp)

	query, _ := json.Marshal(models.Metrics{ID: "Alloc", MType: models.Gauge})
	req = must(http.NewRequest(http.MethodPost, srv.URL+"/value", bytes.NewReader(query)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	resp = do(req)
	fmt.Println("Content-Encoding:", resp.Header.Get("Content-Encoding"))

	// Тело ответа сжато, поэтому его нужно разжать.
	fmt.Println("body:", readGzipBody(resp))

	// Output:
	// compressed request: 200
	// Content-Encoding: gzip
	// body: {"id":"Alloc","type":"gauge","value":2048}
}

// Example: подпись запроса HMAC-SHA256.
//
// Если сервис запущен с ключом (-k/KEY), клиент обязан передать заголовок
// HashSHA256 с HMAC-SHA256 от тела запроса. Сервис отвечает тем же
// заголовком, подписывая тело ответа.
func Example_practicalTrack_hashMiddleware() {
	h := handler.NewHandler(storage.NewMemStorage())
	const key = "secret-key"
	h.SetKey(key)

	r := chi.NewRouter()
	r.Use(h.GzipMiddleware)
	r.Use(h.HashMiddleware)
	r.Post("/update", h.UpdateJSONHandler)

	srv := httptest.NewServer(r)
	defer srv.Close()

	value := 42.5
	body, _ := json.Marshal(models.Metrics{ID: "temperature", MType: models.Gauge, Value: &value})

	req := must(http.NewRequest(http.MethodPost, srv.URL+"/update", bytes.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HashSHA256", hash.Compute(body, key))

	resp := do(req)
	fmt.Println("signed:", resp.StatusCode)
	fmt.Println("response hash ok:", resp.Header.Get("HashSHA256") == hash.Compute([]byte(readBody(resp)), key))

	req = must(http.NewRequest(http.MethodPost, srv.URL+"/update", bytes.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HashSHA256", "deadbeef")

	resp = do(req)
	fmt.Println("wrong hash:", resp.StatusCode)
	closeBody(resp)

	// Output:
	// signed: 200
	// response hash ok: true
	// wrong hash: 400
}

// Example: HTML-страница со всеми метриками.
//
// GET / отдаёт страницу с таблицами gauge- и counter-метрик.
func Example_practicalTrack_rootPage() {
	srv := newServer()
	defer srv.Close()

	resp := do(must(http.NewRequest(http.MethodPost, srv.URL+"/update/gauge/temperature/42.5", nil)))
	closeBody(resp)
	resp = do(must(http.NewRequest(http.MethodPost, srv.URL+"/update/counter/requests_total/7", nil)))
	closeBody(resp)

	resp = do(must(http.NewRequest(http.MethodGet, srv.URL+"/", nil)))
	page := readBody(resp)

	fmt.Println("status:", resp.StatusCode)
	fmt.Println("content-type:", resp.Header.Get("Content-Type"))
	fmt.Println("has gauge row:", strings.Contains(page, "<tr><td>temperature</td><td>42.5</td></tr>"))
	fmt.Println("has counter row:", strings.Contains(page, "<tr><td>requests_total</td><td>7</td></tr>"))

	// Output:
	// status: 200
	// content-type: text/html
	// has gauge row: true
	// has counter row: true
}

// Example: проверка соединения с базой данных.
//
// GET /ping отвечает 200, только когда сервис запущен с хранилищем
// PostgreSQL и соединение живо. Без БД возвращается 500.
func Example_practicalTrack_ping() {
	h := handler.NewHandler(storage.NewMemStorage())
	r := chi.NewRouter()
	r.Get("/ping", h.PingHandler)

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp := do(must(http.NewRequest(http.MethodGet, srv.URL+"/ping", nil)))
	fmt.Println("without db:", resp.StatusCode)
	closeBody(resp)

	// Output:
	// without db: 500
}

// Example: аудит изменений метрик.
//
// Если хендлеру передан Subject аудита, каждое успешное изменение метрики
// порождает событие с именем метрики и IP клиента.
func Example_practicalTrack_audit() {
	h := handler.NewHandler(storage.NewMemStorage())

	subject := audit.NewSubject()
	events := make(chan audit.Event, 1)
	subject.Register(eventObserver{events})
	h.SetAudit(subject)

	r := chi.NewRouter()
	r.Use(h.GzipMiddleware)
	r.Post("/update/{type}/{name}/{value}", h.UpdateHandler)

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp := do(must(http.NewRequest(http.MethodPost, srv.URL+"/update/gauge/temperature/42.5", nil)))
	closeBody(resp)

	event := <-events
	fmt.Println("metrics:", event.Metrics)
	fmt.Println("ip empty:", event.IPAddress == "")

	// Output:
	// metrics: [temperature]
	// ip empty: false
}

// eventObserver — простейший приёмник аудита для примера: складывает
// события в канал, чтобы их можно было проверить без файлов и сети.
type eventObserver struct {
	events chan<- audit.Event
}

func (o eventObserver) Notify(event audit.Event) error {
	o.events <- event
	return nil
}

// must паникует при ошибке: в примерах ошибки конструирования запроса
// означают ошибку самого примера.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
