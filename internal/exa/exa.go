package exa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultBaseURL = "https://api.exa.ai"
const defaultTimeout = 30 * time.Second

type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

type ClientConfig struct {
	APIKey  string
	BaseURL string

	// Optional. If set, used only when HTTPClient is nil.
	Timeout time.Duration

	HTTPClient *http.Client
}

func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("missing EXA_API_KEY")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	hc := cfg.HTTPClient
	if hc == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		hc = &http.Client{Timeout: timeout}
	}

	return &Client{
		apiKey:  cfg.APIKey,
		baseURL: baseURL,
		http:    hc,
	}, nil
}

type ContentsRequest struct {
	URLs []string `json:"urls"`

	// When true, Exa returns extracted page text where available.
	// Can also be an object for advanced options.
	Text any `json:"text,omitempty"`

	Highlights *HighlightsOptions `json:"highlights,omitempty"`
	Summary    *SummaryOptions    `json:"summary,omitempty"`

	Livecrawl        string `json:"livecrawl,omitempty"`
	LivecrawlTimeout int    `json:"livecrawlTimeout,omitempty"`
	MaxAgeHours      *int   `json:"maxAgeHours,omitempty"`
}

type ContentsResponse struct {
	RequestID string            `json:"requestId"`
	Results   []ResultWithContent `json:"results"`
	Context   string            `json:"context"`
	Statuses  []ContentStatus    `json:"statuses"`
}

type ResultWithContent struct {
	ID            string  `json:"id"`
	URL           string  `json:"url"`
	Title         string   `json:"title"`
	Author        *string  `json:"author"`
	PublishedDate *string  `json:"publishedDate"`
	Text          string   `json:"text"`
	Highlights    []string `json:"highlights"`
	Summary       json.RawMessage `json:"summary"`
}

type ContentStatus struct {
	ID     string       `json:"id"`
	Status string       `json:"status"`
	Error  *StatusError `json:"error,omitempty"`
}

type StatusError struct {
	Tag           string `json:"tag,omitempty"`
	HTTPStatusCode *int   `json:"httpStatusCode,omitempty"`
}

type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("exa api error: status=%d", e.StatusCode)
}

func (c *Client) Contents(ctx context.Context, req ContentsRequest) (*ContentsResponse, error) {
	if len(req.URLs) == 0 {
		return nil, fmt.Errorf("exa contents: no urls provided")
	}

	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("exa contents: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/contents", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("exa contents: create request: %w", err)
	}
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("exa contents: request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("exa contents: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(raw)}
	}

	var parsed ContentsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("exa contents: unmarshal response: %w", err)
	}
	return &parsed, nil
}

type HighlightsOptions struct {
	NumSentences     int    `json:"numSentences,omitempty"`
	HighlightsPerURL int    `json:"highlightsPerUrl,omitempty"`
	Query            string `json:"query,omitempty"`
}

type SummaryOptions struct {
	Query  string `json:"query,omitempty"`
	Schema any    `json:"schema,omitempty"`
}

type AnswerRequest struct {
	Query  string `json:"query"`
	Text   bool   `json:"text,omitempty"`
	Stream bool   `json:"stream,omitempty"`
}

type AnswerResponse struct {
	Answer    string          `json:"answer"`
	Citations []AnswerCitation `json:"citations"`
}

type AnswerCitation struct {
	ID            string  `json:"id"`
	URL           string  `json:"url"`
	Title         string  `json:"title"`
	Author        *string `json:"author"`
	PublishedDate *string `json:"publishedDate"`
	Text          string  `json:"text"`
}

func (c *Client) Answer(ctx context.Context, req AnswerRequest) (*AnswerResponse, error) {
	if req.Query == "" {
		return nil, fmt.Errorf("exa answer: missing query")
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("exa answer: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/answer", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("exa answer: create request: %w", err)
	}
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("exa answer: request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("exa answer: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(raw)}
	}

	var parsed AnswerResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("exa answer: unmarshal response: %w", err)
	}
	return &parsed, nil
}

