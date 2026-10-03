// Package profiler поднимает отдельный HTTP-сервер с эндпоинтами
// net/http/pprof. Он используется для снятия профилей памяти и CPU
// с работающего сервиса (в том числе под нагрузкой).
package profiler

import (
	"net"
	"net/http"
	"net/http/pprof"
)

// Handler возвращает mux с профилировщиком. Отделён от Start, чтобы
// его можно было использовать в тестах и встраивать в другие серверы.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}

// Start слушает addr и обслуживает эндпоинты pprof в отдельной горутине.
// Ошибка открытия порта возвращается синхронно, чтобы сервис не стартовал
// с недоступным профилировщиком.
func Start(addr string) (*http.Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	srv := &http.Server{Addr: ln.Addr().String(), Handler: Handler()}
	go func() {
		_ = srv.Serve(ln)
	}()

	return srv, nil
}
