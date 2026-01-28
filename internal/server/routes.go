package server

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

func (s *Server) RegisterRoutes() http.Handler {
	r := chi.NewRouter()

	r.Get("/health", s.healthHandler)

	api := chi.NewRouter()
	api.Use(middleware.Logger)
	api.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	api.Get("/", s.HelloWorldHandler)

	// TODO: add pagination
	// took parts from chi example: https://github.com/go-chi/chi/blob/master/_examples/rest/main.go
	api.Route("/articles", func(r chi.Router) {
		r.With(Paginate).Get("/", s.GetArticlesPageHandler)
		r.Post("/", s.CreateArticle)
		r.Get("/all", s.GetAllArticlesHandler)

	})

	r.Mount("/", api)

	return r
}

func (s *Server) HelloWorldHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	resp := map[string]map[string]string{
		"GET /articles": {
			"accepts":     "N/A",
			"returns":     `[{id: integer, title: string, author: string, summary: string, dateRead: string, datePublished: string, link: string, img_path: string, type: integer}]`,
			"description": "Returns all the articles",
		},
		"POST /articles": {
			"accepts":     `{articleLink: string}`,
			"returns":     `{id: integer, title: string, author: string, summary: string, dateRead: string, datePublished: string, link: string, img_path: string, type: integer}`,
			"description": "Adds a new article using the provided link and returns the saved article metadata",
		},
		"GET /health": {
			"accepts":     "N/A",
			"returns":     "Database health status",
			"description": "Returns the health status of the database",
		},
	}

	jsonResp, err := json.Marshal(resp)
	if err != nil {
		log.Printf("Error handling JSON marshal: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Write(jsonResp)
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	jsonResp, _ := json.Marshal(s.db.Health())
	_, _ = w.Write(jsonResp)
}
