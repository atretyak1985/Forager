package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/swarmery/forager/internal/llm"
)

// Fetch downloads a page and returns readable plain text,
// truncated to MaxChars to protect the model's context window.
type Fetch struct {
	MaxChars int
	HTTP     *http.Client
}

func NewFetch(maxChars int) *Fetch {
	if maxChars <= 0 {
		maxChars = 12000
	}
	return &Fetch{
		MaxChars: maxChars,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (f *Fetch) Definition() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name: "fetch_page",
			Description: "Fetch a web page by URL and return its readable text content. " +
				"Use this after web_search to read the full article behind a result. " +
				"Long pages are truncated; use the offset parameter to read further.",
			Parameters: mustSchema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "Full URL including https://",
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": "Character offset to continue reading a truncated page (default 0)",
					},
				},
				"required": []string{"url"},
			}),
		},
	}
}

type fetchArgs struct {
	URL    string `json:"url"`
	Offset int    `json:"offset"`
}

var (
	reScript = regexp.MustCompile(`(?is)<(script|style|noscript|svg|head|nav|footer|form|iframe)[^>]*>.*?</\s*(script|style|noscript|svg|head|nav|footer|form|iframe)\s*>`)
	reBlock  = regexp.MustCompile(`(?i)</?(p|div|br|h[1-6]|li|tr|section|article|blockquote|pre)[^>]*>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]+>`)
	reNL     = regexp.MustCompile(`\n{3,}`)
	reSpaces = regexp.MustCompile(`[ \t]{2,}`)
)

func (f *Fetch) Call(ctx context.Context, argsJSON string) (string, error) {
	var args fetchArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	u := strings.TrimSpace(args.URL)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return "", fmt.Errorf("url must start with http:// or https://")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; forager/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,*/*")

	resp, err := f.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("page returned status %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(ct, "text/") && !strings.Contains(ct, "html") &&
		!strings.Contains(ct, "json") && !strings.Contains(ct, "xml") {
		return "", fmt.Errorf("unsupported content type %q (binary content)", ct)
	}

	// Hard cap on raw download: 4 MB.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	text := htmlToText(string(raw))
	if text == "" {
		return "Page fetched but no readable text was extracted.", nil
	}

	// Windowed output with offset for pagination.
	start := args.Offset
	if start < 0 {
		start = 0
	}
	if start >= len(text) {
		return fmt.Sprintf("Offset %d is beyond end of content (total %d chars).", start, len(text)), nil
	}
	end := start + f.MaxChars
	truncated := false
	if end > len(text) {
		end = len(text)
	} else {
		truncated = true
	}
	// Avoid slicing inside a UTF-8 rune.
	for start > 0 && start < len(text) && !isRuneStart(text[start]) {
		start++
	}
	for end > start && end < len(text) && !isRuneStart(text[end]) {
		end--
	}

	out := text[start:end]
	if truncated {
		out += fmt.Sprintf("\n\n[content truncated: showing chars %d-%d of %d; call fetch_page again with offset=%d to continue]",
			start, end, len(text), end)
	}
	return out, nil
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func htmlToText(s string) string {
	s = reScript.ReplaceAllString(s, " ")
	s = reBlock.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, " ")
	s = htmlUnescape(s)
	s = reSpaces.ReplaceAllString(s, " ")

	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			kept = append(kept, ln)
		}
	}
	s = strings.Join(kept, "\n")
	s = reNL.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

var htmlEntities = strings.NewReplacer(
	"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">",
	"&quot;", `"`, "&#39;", "'", "&apos;", "'", "&mdash;", "—",
	"&ndash;", "–", "&hellip;", "…", "&laquo;", "«", "&raquo;", "»",
)

func htmlUnescape(s string) string { return htmlEntities.Replace(s) }
