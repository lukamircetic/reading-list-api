package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reading-list-api/internal/exa"
	"reading-list-api/internal/types"
	"strconv"
	"strings"
	"time"

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

	// 3 - extract article metadata using Exa
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
	const (
		exaTimeout           = 90 * time.Second
		exaLivecrawlTimeout  = 20000 // ms
		exaMaxTextCharacters = 12000
	)

	ctx, cancel := context.WithTimeout(context.Background(), exaTimeout)
	defer cancel()

	exaClient, err := exa.NewClient(exa.ClientConfig{
		APIKey:  os.Getenv("EXA_API_KEY"),
		Timeout: exaTimeout,
	})
	if err != nil {
		return nil, err
	}

	extracted := (*extractedArticleDetails)(nil)

	contents, err := exaClient.Contents(ctx, exa.ContentsRequest{
		URLs: []string{articleLink},
		Text: map[string]any{
			"maxCharacters":   exaMaxTextCharacters,
			"includeHtmlTags": false,
		},
		Summary: &exa.SummaryOptions{
			Query:  exaExtractionRulesPrompt(),
			Schema: exaExtractionSchema(),
		},
		Livecrawl:        "preferred",
		LivecrawlTimeout: exaLivecrawlTimeout,
	})
	if err != nil {
		return nil, err
	}

	if err := exaValidateStatuses(articleLink, contents.Statuses); err != nil {
		return nil, err
	}

	if len(contents.Results) == 0 {
		return nil, fmt.Errorf("exa contents: no results returned")
	}

	res := contents.Results[0]

	parsed, err := parseExtractedDetails(res.Summary)
	if err == nil {
		extracted = parsed
	} else {
		parsed, ansErr := exaExtractViaAnswer(ctx, exaClient, articleLink)
		if ansErr != nil {
			return nil, fmt.Errorf("exa extraction failed: %w", ansErr)
		}
		extracted = parsed
	}

	title := strings.TrimSpace(extracted.Title)
	author := strings.TrimSpace(extracted.Author)
	summary := strings.TrimSpace(extracted.Summary)
	published := strings.TrimSpace(extracted.DatePublished)
	kind := extracted.Type

	if author == "" {
		author = fallbackAuthorFromURL(articleLink)
	}

	if title == "" || summary == "" {
		return nil, fmt.Errorf("exa extraction incomplete: title=%t summary=%t", title != "", summary != "")
	}

	return &types.Article{
		Title:         title,
		Author:        author,
		Summary:       summary,
		DatePublished: published,
		Type:          kind,
		DateRead:      time.Now().Format("2006-01-02"),
		Link:          articleLink,
	}, nil
}

type extractedArticleDetails struct {
	Title         string `json:"title"`
	Author        string `json:"author"`
	Summary       string `json:"summary"`
	DatePublished string `json:"datePublished"`
	Type          int    `json:"type"`
}

func parseExtractedDetails(raw json.RawMessage) (*extractedArticleDetails, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty summary")
	}

	// Could be an object OR a string (possibly containing JSON).
	if raw[0] == '{' {
		var out extractedArticleDetails
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		return &out, nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return parseExtractedDetailsFromString(s)
}

func parseExtractedDetailsFromString(s string) (*extractedArticleDetails, error) {
	obj, err := extractJSONObjectFromString(s)
	if err != nil {
		return nil, err
	}
	var out extractedArticleDetails
	if err := json.Unmarshal([]byte(obj), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func extractJSONObjectFromString(s string) (string, error) {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < 0 || end <= start {
		return "", fmt.Errorf("could not find json object in string")
	}
	return s[start : end+1], nil
}

func exaExtractionRulesPrompt() string {
	// Keep this aligned with the DB fields. We still apply local guardrails (summary length/date format)
	// even if the model drifts.
	return strings.TrimSpace(`
Extract article metadata from the provided URL and return ONLY a single JSON object matching the provided JSON Schema.

Rules:
- title: full title.
- author: author(s), comma-separated if multiple; if unknown return "".
- summary: single sentence around 20 words or less.
- datePublished: YYYY-MM-DD if possible; otherwise YYYY-MM; otherwise YYYY; otherwise "".
- type: 0=article, 1=academic/research paper, 2=book, -1=not one of these.
`)
}

func exaExtractionSchema() map[string]any {
	return map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type":    "object",
		"properties": map[string]any{
			"title": map[string]any{
				"type":        "string",
				"description": "Full title of the article, book, or paper.",
			},
			"author": map[string]any{
				"type":        "string",
				"description": "Author(s). If multiple, comma-separated. If unknown, empty string.",
			},
			"summary": map[string]any{
				"type":        "string",
				"description": "Single sentence summary ~20 words or less.",
			},
			"datePublished": map[string]any{
				"type":        "string",
				"description": "YYYY-MM-DD if possible; otherwise YYYY-MM; otherwise YYYY; otherwise empty string.",
			},
			"type": map[string]any{
				"type":        "integer",
				"description": "0=article, 1=academic/research paper, 2=book, -1=not one of these.",
			},
		},
		"required": []string{"title", "author", "summary", "datePublished", "type"},
	}
}

func exaValidateStatuses(articleLink string, statuses []exa.ContentStatus) error {
	for _, st := range statuses {
		if st.ID == articleLink && strings.EqualFold(st.Status, "error") {
			if st.Error != nil && st.Error.Tag != "" {
				return fmt.Errorf("exa contents: %s", st.Error.Tag)
			}
			return fmt.Errorf("exa contents: failed to fetch content")
		}
	}
	return nil
}

func exaExtractViaAnswer(ctx context.Context, exaClient *exa.Client, articleLink string) (*extractedArticleDetails, error) {
	answerResp, err := exaClient.Answer(ctx, exa.AnswerRequest{
		Query: fmt.Sprintf(
			`From this URL: %s
Return ONLY a single JSON object (no prose, no markdown fences) matching:
{"title": string, "author": string, "summary": string, "datePublished": string, "type": number}

Rules:
- title: full title.
- author: author(s), comma-separated if multiple; if unknown return "".
- summary: single sentence around 20 words or less.
- datePublished: YYYY-MM-DD if possible; otherwise YYYY-MM; otherwise YYYY; otherwise "".
- type: 0=article, 1=academic/research paper, 2=book, -1=not one of these.`,
			articleLink,
		),
		Text: false,
	})
	if err != nil {
		return nil, err
	}
	return parseExtractedDetailsFromString(answerResp.Answer)
}


func fallbackAuthorFromURL(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	if host == "" {
		return ""
	}

	parts := strings.Split(host, ".")
	if len(parts) >= 2 {
		// crude but effective for most domains (e.g. blog.railway.com -> railway)
		return parts[len(parts)-2]
	}
	return host
}