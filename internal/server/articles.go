package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"reading-list-api/internal/types"
	"regexp"
	"strconv"
	"time"

	"github.com/go-chi/render"
	"google.golang.org/genai"
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

	// 3 - get and validate article metadata using gemini
	var article *types.Article
	var extractionErr error
	numRetries, err := strconv.Atoi(os.Getenv("NUM_RETRIES"))
	if err != nil {
		render.Render(w, r, ErrInternalServer(err))
		return
	}

	// gemini search is flaky and sometimes doesn't run the prompt with search, so retry if that's the case
	for attempt := range numRetries {
		article, extractionErr = extractArticleMetadata(articleLink)
		if extractionErr != nil {
			render.Render(w, r, ErrInternalServer(extractionErr))
			return
		}

		if article.Type >= 0 {
			break
		}

		if article.Type == -1 {
			render.Render(w, r, ErrInvalidRequest(fmt.Errorf("link supplied is not an article or book")))
			return
		}

		if article.Type == -2 {
			if attempt == numRetries-1 {
				render.Render(w, r, ErrInvalidRequest(fmt.Errorf("unable to get gemini to use search... please try again")))
				return
			}
			continue
		}
	}

	// 4 - create a db record for this article and populate all the fields
	err = s.db.InsertArticle(article)
	if err != nil {
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
	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  os.Getenv("GEMINI_API_KEY"),
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Println("could not connect to gemini", err)
		return nil, err
	}

	// source: https://github.com/google/generative-ai-go/issues/229
	config := &genai.GenerateContentConfig{
		Tools: []*genai.Tool{
			{
				GoogleSearch: &genai.GoogleSearch{},
			},
		},
	}

	prompt := fmt.Sprintf(`
		Please find the following information about the content at this URL: %s Use web search to find the information.
		Extract and provide the following information using this JSON schema:
		- title: (Extract the full title of the article, book, or paper)
		- author: (Extract the author(s) of the content. If you can't find the author's name in the post itself, look around the website to try and find it - common places are in the header, footer or below the title. If you still can't find the author write "")
		- summary: (Provide a concise, single-sentence summary capturing the main topic or argument of the content.)
		- datePublished: (Provide the publication date in YYYY-MM-DD format if possible. If only the year or month and year are available, provide those. If the date is not found, write "")
		- type: (Please specify the enum value for the content type; 0 is for article, 1 is for academic/research paper, 2 is for book, if the provided url is not one of these types of content write -1, if you were unable to search the web for some reason write -2)
		`, articleLink,
	)

	stream := client.Models.GenerateContentStream(
		ctx,
		"gemini-2.0-flash",
		genai.Text(prompt),
		config,
	)

	geminiContent := ""
	for result, err := range stream {
		if err != nil {
			log.Println("prompt failed", err)
			return nil, err
		}
		geminiContent = result.Candidates[0].Content.Parts[0].Text
	}

	re := regexp.MustCompile(`(?m)^(?s){(.*)}$`)
	cleanedString := re.FindString(geminiContent)
	if cleanedString == "" {
		return nil, fmt.Errorf("could not parse gemini content with regex")
	}

	// use for debugging
	// fmt.Println(cleanedString)

	var geminiArticleMetadata GeminiArticleDetails

	err = json.Unmarshal([]byte(cleanedString), &geminiArticleMetadata)
	if err != nil {
		fmt.Println("error unmarshalling", err)
		return nil, err
	}

	// create an article with a bunch of stuff
	article := &types.Article{
		Title:         geminiArticleMetadata.Title,
		Author:        geminiArticleMetadata.Author,
		Summary:       geminiArticleMetadata.Summary,
		DatePublished: geminiArticleMetadata.DatePublished,
		Type:          geminiArticleMetadata.Type,
		DateRead:      time.Now().Format("2006-01-02"),
		Link:          articleLink,
	}

	return article, nil
}

// generate content stream investigate
type GeminiArticleDetails struct {
	Title         string `json:"title"`
	Author        string `json:"author"`
	Summary       string `json:"summary"`
	DatePublished string `json:"datePublished"`
	Type          int    `json:"type"`
}

/* Keeping this schema here on the off chance that they fix this for 2.0-flash
model.ResponseMIMEType = "application/json"
responseSchema := &genai.Schema{
	Type: genai.TypeObject,
	Properties: map[string]*genai.Schema{
		"title":         {Type: genai.TypeString},
		"author":        {Type: genai.TypeString},
		"summary":       {Type: genai.TypeString},
		"datePublished": {Type: genai.TypeString},
		"type":          {Type: genai.TypeString},
	},
	Required: []string{"title", "author", "summary", "type"},
}
var dynamicThreshold float32 = 0.6

config := &genai.GenerateContentConfig{
	// Response Schema isn't supported with GenerateContentStream, but GenerateContent doesn't support Search...
	ResponseMIMEType: "application/json",
	ResponseSchema:   responseSchema,
	Tools: []*genai.Tool{
		{
			// For some reason Retrieval is not supported, yet it's in the interface...
			GoogleSearchRetrieval: &genai.GoogleSearchRetrieval{
				DynamicRetrievalConfig: &genai.DynamicRetrievalConfig{
					DynamicThreshold: &dynamicThreshold,
				},
			},
		},
	},
}
*/
