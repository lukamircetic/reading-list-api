package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
const defaultOpenRouterTimeout = 120 * time.Second

type OpenRouterClient struct {
	apiKey  string
	model   string
	baseURL string
	http    *http.Client
}

type OpenRouterClientConfig struct {
	APIKey  string
	Model   string
	BaseURL string

	// Optional. If set, used only when HTTPClient is nil.
	Timeout time.Duration

	HTTPClient *http.Client
}

func NewOpenRouterClient(cfg OpenRouterClientConfig) (*OpenRouterClient, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("missing OPENROUTER_API_KEY")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("missing OpenRouter model")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultOpenRouterBaseURL
	}

	hc := cfg.HTTPClient
	if hc == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = defaultOpenRouterTimeout
		}
		hc = &http.Client{Timeout: timeout}
	}

	return &OpenRouterClient{
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		baseURL: baseURL,
		http:    hc,
	}, nil
}

type openAIChatCompletionsRequest struct {
	Model       string                     `json:"model"`
	Messages    []openAIChatMessage        `json:"messages"`
	Temperature *float32                   `json:"temperature,omitempty"`
	MaxTokens   *int                       `json:"max_tokens,omitempty"`
	TopP        *float32                   `json:"top_p,omitempty"`
	Stop        []string                   `json:"stop,omitempty"`
	Metadata    map[string]any             `json:"metadata,omitempty"`
	Extra       map[string]json.RawMessage `json:"-"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatCompletionsResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

type OpenRouterError struct {
	StatusCode int
	Message    string
	Body       string

	RetryAfter time.Duration
}

func (e *OpenRouterError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("openrouter error: status=%d message=%q", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("openrouter error: status=%d", e.StatusCode)
}

func (c *OpenRouterClient) ChatCompletion(ctx context.Context, prompt string) (string, error) {
	reqBody := openAIChatCompletionsRequest{
		Model: c.model,
		Messages: []openAIChatMessage{
			{Role: "user", Content: prompt},
		},
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		ore := &OpenRouterError{
			StatusCode: resp.StatusCode,
			Body:       string(raw),
		}
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			// Retry-After can be seconds or HTTP date; handle seconds best-effort.
			if secs, convErr := strconv.Atoi(ra); convErr == nil {
				ore.RetryAfter = time.Duration(secs) * time.Second
			}
		}

		var parsed openAIChatCompletionsResponse
		if jsonErr := json.Unmarshal(raw, &parsed); jsonErr == nil && parsed.Error != nil && parsed.Error.Message != "" {
			ore.Message = parsed.Error.Message
		}

		return "", ore
	}

	var parsed openAIChatCompletionsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", &OpenRouterError{StatusCode: resp.StatusCode, Message: parsed.Error.Message, Body: string(raw)}
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("openrouter: empty choices")
	}
	return parsed.Choices[0].Message.Content, nil
}
