package main

import (
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/subtotalstew/gometrics.git/internal/handler"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

func main() {
	memstorage := storage.NewMemStorage()
	h := handler.NewHandler(memstorage)
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Post("/update/{type}/{name}/{value}", h.UpdateHandler)
	r.Get("/value/{type}/{name}", h.ValueHandler)
	r.Get("/", h.RootHandler)

	log.Println("Starting server on :8080")
	if err := http.ListenAndServe(":8080", r); err != nil {
		log.Fatal(err)
	}
}
