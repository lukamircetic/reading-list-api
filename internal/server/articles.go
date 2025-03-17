package server

import (
	"log"
	"net/http"

	"github.com/go-chi/render"
)

type Article struct {
	ID        int  `db:"id" json:"id"`
	Title     int  `db:"title" json:"title"`
	Summary   int  `db:"summary" json:"summary"`
	DateRead  int  `db:"date_read" json:"date_read"`
	Link      int  `db:"link" json:"link"`
	ImagePath int  `db:"img_path" json:"img_path"`
	Type      bool `db:"type" json:"type"`
}

type ArticleResponse struct {
	*Article
}

// need article type
// need sqlite db/getter
func (s *Server) GetArticlesHandler(w http.ResponseWriter, r *http.Request) {
	// 1 - query sqlite db for all articles
	articles := make([]Article, 0)
	// 2 - return list of articles as a response
	err := render.RenderList(w, r, NewArticleListResponse(&articles))
	if err != nil {
		log.Println("error rendering article list", err)
	}
}

func NewArticleListResponse(articles *[]Article) []render.Renderer {
	list := []render.Renderer{}

	for _, article := range *articles {
		list = append(list, NewArticleResponse(&article))
	}
	return list
}

func NewArticleResponse(article *Article) *ArticleResponse {
	resp := &ArticleResponse{Article: article}
	return resp
}

func (rd *ArticleResponse) Render(w http.ResponseWriter, r *http.Request) error {
	return nil
}
