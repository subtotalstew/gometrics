package main

import (
	"flag"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog/log"
	"github.com/subtotalstew/gometrics.git/internal/handler"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

func main() {

	var addr string
	flag.StringVar(&addr, "a", "localhost:8080", "address and port to run server, format: <hostname>:<port>")

	flag.Parse()

	if envAddr := os.Getenv("ADDRESS"); envAddr != "" {
		addr = envAddr
	}
	log.Info().Msgf("Starting server on %s", addr)

	memstorage := storage.NewMemStorage()
	h := handler.NewHandler(memstorage)
	r := chi.NewRouter()

	r.Use(h.GzipMiddleware)
	r.Use(h.LoggingMiddleware)
	r.Use(middleware.Recoverer)

	r.Post("/update", h.UpdateJSONHandler)
	r.Post("/value", h.ValueJSONHandler)
	r.Post("/update/", h.UpdateJSONHandler)
	r.Post("/value/", h.ValueJSONHandler)

	r.Post("/update/{type}/{name}/{value}", h.UpdateHandler)
	r.Get("/value/{type}/{name}", h.ValueHandler)
	r.Get("/", h.RootHandler)

	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal().Msg(err.Error())
	}
}
