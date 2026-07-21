package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/swarmery/forager/internal/llm"
)

// Search queries a local SearXNG instance (format=json).
type Search struct {
	BaseURL    string // e.g. http://localhost:8888
	MaxResults int    // default cap when the model doesn't specify
	HTTP       *http.Client
}

func NewSearch(baseURL string, maxResults int) *Search {
	if maxResults <= 0 {
		maxResults = 8
	}
	return &Search{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		MaxResults: maxResults,
		HTTP:       &http.Client{Timeout: 20 * time.Second},
	}
}

func (s *Search) Definition() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name: "web_search",
			Description: "Search the web. Returns a numbered list of results with title, URL and snippet. " +
				"Use specific, short queries (1-6 words). Call fetch_page on a result URL to read the full content.",
			Parameters: mustSchema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query",
					},
					"max_results": map[string]any{
						"type":        "integer",
						"description": "How many results to return (default 8, max 15)",
					},
				},
				"required": []string{"query"},
			}),
		},
	}
}

type searchArgs struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

type searxResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
		Engine  string `json:"engine"`
	} `json:"results"`
}

func (s *Search) Call(ctx context.Context, argsJSON string) (string, error) {
	var args searchArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if strings.TrimSpace(args.Query) == "" {
		return "", fmt.Errorf("query is empty")
	}
	limit := args.MaxResults
	if limit <= 0 || limit > 15 {
		limit = s.MaxResults
	}

	q := url.Values{}
	q.Set("q", args.Query)
	q.Set("format", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.BaseURL+"/search?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("searxng request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return "", fmt.Errorf("searxng returned %d: %s (is format=json enabled in settings.yml?)",
			resp.StatusCode, string(body))
	}

	var sr searxResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", fmt.Errorf("decode searxng response: %w", err)
	}
	if len(sr.Results) == 0 {
		return "No results found. Try a different, broader query.", nil
	}

	var b strings.Builder
	for i, r := range sr.Results {
		if i >= limit {
			break
		}
		fmt.Fprintf(&b, "%d. %s\n   URL: %s\n", i+1, strings.TrimSpace(r.Title), r.URL)
		if c := strings.TrimSpace(r.Content); c != "" {
			fmt.Fprintf(&b, "   %s\n", c)
		}
	}
	return b.String(), nil
}
