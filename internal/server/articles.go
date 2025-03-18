package server

import (
	"net/http"
	"reading-list-api/internal/types"

	"github.com/go-chi/render"
)

type ArticleResponse struct {
	*types.Article
}

func (s *Server) GetArticlesHandler(w http.ResponseWriter, r *http.Request) {
	// 1 - query sqlite db for all articles
	articles, err := s.db.GetAllArticles()
	if err != nil {
		render.Render(w, r, ErrInternalServer(err))
		return
	}
	// 2 - return list of articles as a response
	err = render.RenderList(w, r, NewArticleListResponse(articles))
	if err != nil {
		render.Render(w, r, ErrRender(err))
		return
	}
}

func NewArticleListResponse(articles *[]types.Article) []render.Renderer {
	list := []render.Renderer{}

	for _, article := range *articles {
		list = append(list, NewArticleResponse(&article))
	}
	return list
}

func NewArticleResponse(article *types.Article) *ArticleResponse {
	resp := &ArticleResponse{Article: article}
	return resp
}

func (rd *ArticleResponse) Render(w http.ResponseWriter, r *http.Request) error {
	return nil
}
