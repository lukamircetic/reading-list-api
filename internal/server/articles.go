package server

import (
	"errors"
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

func (s *Server) CreateArticle(w http.ResponseWriter, r *http.Request) {
	// 1 - extract the article link from the request body
	data := &ArticleRequest{}
	err := render.Bind(r, data)
	if err != nil {
		render.Render(w, r, ErrInvalidRequest((err)))
		return
	}
	// 1.5 - check if the link already exists in the db

	// articleLink := data.ArticleLink
	// err := s.db.articleExists(articleLink)

	// 2 - send the article for extraction to gemini
	// 3 - parse the gemini response (some form of json)
	// 4 - return if the link isn't a article
	// 5 - create a db record for this article and populate all the fields
	// 6 - return 200
}

type ArticleRequest struct {
	ArticleLink string `json:"articleLink"`
}

func (a *ArticleRequest) Bind(r *http.Request) error {
	// a.ArticleLink is nil if no Article fields are sent in the request. Return an
	// error to avoid a nil pointer dereference.
	if a.ArticleLink == "" {
		return errors.New("missing required Article fields")
	}

	return nil
}
