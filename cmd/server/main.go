package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/subtotalstew/gometrics.git/internal/handler"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

func main() {
	var addr string
	flag.StringVar(&addr, "a", "localhost:8080", "address and port to run server, format: <hostname>:<port>")

	flag.Parse()

	if flag.NFlag() > 1 {
		flag.Usage()
		log.Fatal("Check startup arguments!!...startup Failed.")
	}

	log.Printf("Starting server on %s", addr)

	memstorage := storage.NewMemStorage()
	h := handler.NewHandler(memstorage)
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Post("/update/{type}/{name}/{value}", h.UpdateHandler)
	r.Get("/value/{type}/{name}", h.ValueHandler)
	r.Get("/", h.RootHandler)

	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}
