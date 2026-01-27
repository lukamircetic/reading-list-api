package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reading-list-api/internal/llm"
	"reading-list-api/internal/types"
	"regexp"
	"strconv"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/go-chi/render"
)

type contextKey string

const (
	PageCtxKey     contextKey = "Page"
	PageSizeCtxKey contextKey = "PageSize"
)

type ArticleResponse struct {
	*types.Article
}

type ArticlePageResponse struct {
	TotalArticles int             `json:"totalArticles"`
	Articles      []types.Article `json:"articles"`
}

func Paginate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 1
		pageSize := 10

		query := r.URL.Query()

		pageStr := query.Get("page")
		if pageStr != "" {
			var err error
			page, err = strconv.Atoi(pageStr)
			if err != nil || page < 1 {
				render.Render(w, r, ErrInvalidRequest(fmt.Errorf("invalid page number: %s", pageStr)))
				return
			}
		}

		ctx := context.WithValue(r.Context(), PageCtxKey, page)
		ctx = context.WithValue(ctx, PageSizeCtxKey, pageSize)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) GetArticlesPageHandler(w http.ResponseWriter, r *http.Request) {
	// 0 - get the pagination info
	page := r.Context().Value(PageCtxKey).(int)
	pageSize := r.Context().Value(PageSizeCtxKey).(int)

	// 0.5 get total number of articles in db
	total, err := s.db.GetArticleCount()
	if err != nil {
		render.Render(w, r, ErrInternalServer(fmt.Errorf("error getting total article count: %v", err)))
		return
	}

	offset := (page - 1) * pageSize

	if offset < 0 || offset >= total || total == 0 {
		empty := make([]types.Article, 0)
		err = render.Render(w, r, NewArticlePageResponse(&empty, total))
		if err != nil {
			render.Render(w, r, ErrRender(err))
		}
		return
	}

	// 1 - query sqlite db for all articles
	pageArticles, err := s.db.GetArticlePage(offset, pageSize)
	if err != nil {
		render.Render(w, r, ErrInternalServer(err))
		return
	}
	// fmt.Println("pages", len(*pageArticles), *pageArticles)

	// 2 - return list of articles as a response
	err = render.Render(w, r, NewArticlePageResponse(pageArticles, total))
	if err != nil {
		render.Render(w, r, ErrRender(err))
		return
	}
}

func NewArticlePageResponse(articles *[]types.Article, totalArticles int) *ArticlePageResponse {
	var articlePageList []types.Article
	if len(*articles) == 0 {
		articlePageList = make([]types.Article, 0)
	} else {
		articlePageList = *articles
	}
	resp := &ArticlePageResponse{
		TotalArticles: totalArticles,
		Articles:      articlePageList,
	}
	return resp
}

func (rd *ArticlePageResponse) Render(w http.ResponseWriter, r *http.Request) error {
	return nil
}

func (s *Server) GetAllArticlesHandler(w http.ResponseWriter, r *http.Request) {
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
		fmt.Println("error decoding request data", err)
		render.Render(w, r, ErrInvalidRequest((err)))
		return
	}
	// 2 - check if the link already exists in the db
	articleLink := data.ArticleLink
	exists, err := s.db.ArticleExists(articleLink)
	if err != nil {
		render.Render(w, r, ErrInternalServer(err))
		return
	}
	if exists {
		render.Render(w, r, ErrInvalidRequest(fmt.Errorf("article exists in db")))
		return
	}

	// 3 - extract article metadata using OpenRouter (DeepSeek)
	article, err := extractArticleMetadata(articleLink)
	if err != nil {
		render.Render(w, r, ErrInternalServer(err))
		return
	}

	if article.Type == -1 {
		render.Render(w, r, ErrInvalidRequest(fmt.Errorf("link supplied is not an article or book")))
		return
	}

	// 4 - create a db record for this article and populate all the fields
	err = s.db.InsertArticle(article)
	if err != nil {
		fmt.Println("error inserting article to db", err)
		render.Render(w, r, ErrInternalServer(err))
		return
	}

	// 5 - return posted article
	err = render.Render(w, r, NewArticleResponse(article))
	if err != nil {
		render.Render(w, r, ErrRender(err))
		return
	}

}

type ArticleRequest struct {
	ArticleLink string `json:"articleLink"`
}

func (a *ArticleRequest) Bind(r *http.Request) error {
	if a.ArticleLink == "" {
		return errors.New("missing required Article fields")
	}

	return nil
}

func extractArticleMetadata(articleLink string) (*types.Article, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	orClient, err := llm.NewOpenRouterClient(llm.OpenRouterClientConfig{
		APIKey:  os.Getenv("OPENROUTER_API_KEY"),
		Model:   "deepseek/deepseek-r1-0528:free",
		Timeout: 120 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	markdown, err := getArticleAsMarkdown(articleLink)
	if err != nil {
		fmt.Println("error getting article as markdown: ", err)
		return nil, err
	}
	prompt := fmt.Sprintf(`
You are given extracted page content from a URL. Return ONLY a single JSON object (no prose, no markdown fences) matching this schema exactly:
{
  "title": string,
  "author": string,
  "summary": string,
  "datePublished": string,
  "type": number
}

Rules:
- "title": you mustextract the full title of the article, book, or paper.
- "author" You must extract the author(s) of the content. If it's not obvious make assumptions from the blog name. If there are multiple authors, please return them comma-separated in a single string. If you still can't find the author name write ""
- "summary": must be a single sentence around 20 words or less.
- "datePublished": should be YYYY-MM-DD if possible; otherwise YYYY-MM; otherwise YYYY; otherwise empty string.
- "type": 0=article, 1=academic/research paper, 2=book, -1=not one of these.

Page content:
%s
		`, *markdown,
	)

	content, err := orClient.ChatCompletion(ctx, prompt)
	if err != nil {
		fmt.Println("prompt failed", err)
		return nil, err
	}

	cleanedString, err := extractJSONObject(content)
	if err != nil {
		fmt.Println("model content", content)
		return nil, err
	}

	var extracted extractedArticleDetails
	if err := json.Unmarshal([]byte(cleanedString), &extracted); err != nil {
		fmt.Println("error unmarshalling", err)
		return nil, err
	}

	// create an article with a bunch of stuff
	article := &types.Article{
		Title:         extracted.Title,
		Author:        extracted.Author,
		Summary:       extracted.Summary,
		DatePublished: extracted.DatePublished,
		Type:          extracted.Type,
		DateRead:      time.Now().Format("2006-01-02"),
		Link:          articleLink,
	}

	return article, nil
}

type extractedArticleDetails struct {
	Title         string `json:"title"`
	Author        string `json:"author"`
	Summary       string `json:"summary"`
	DatePublished string `json:"datePublished"`
	Type          int    `json:"type"`
}

func extractJSONObject(s string) (string, error) {
	// Best-effort: many models sometimes add preambles. Extract the outermost JSON object.
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < 0 || end <= start {
		// Keep legacy regex around for unusual formatting.
		re := regexp.MustCompile(`(?m)^(?s){(.*)}$`)
		cleaned := re.FindString(s)
		if cleaned == "" {
			return "", fmt.Errorf("error could not parse json object from model output")
		}
		return cleaned, nil
	}
	return s[start : end+1], nil
}

func getArticleAsMarkdown(url string) (*string, error) {
	client := &http.Client{}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Println("error creating http request", err)
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")
	resp, err := client.Do(req)

	if err != nil {
		fmt.Println("error requesting url", err)
		return nil, fmt.Errorf("error executing request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	if err != nil {
		fmt.Println("error reading resp body", err)
		return nil, fmt.Errorf("error reading resp body: %v", err)
	}

	content := string(body)
	// fmt.Println("content", content)
	markdown, err := htmltomarkdown.ConvertString(content)
	if err != nil {
		fmt.Println("error converting to markdown", err)
		return nil, fmt.Errorf("error converting to markdown: %v", err)
	}

	// fmt.Println("markdown", markdown)
	return &markdown, nil
}
