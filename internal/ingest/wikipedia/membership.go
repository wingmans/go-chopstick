// Package wikipedia reads the temporary Wikipedia constituent source.
package wikipedia

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"

	"wingman.com/chopstick/internal/constituents"
)

const DefaultURL = "https://en.wikipedia.org/wiki/List_of_S%26P_500_companies"

var tickerPattern = regexp.MustCompile(`^[A-Z][A-Z0-9.-]*$`)

// FetchMembership returns the ticker membership published in the source
// table. The caller remains responsible for resolving stable company data.
func FetchMembership(ctx context.Context, client *http.Client, sourceURL,
	userAgent string,
) ([]string, error) {
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, sourceURL, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create Wikipedia request: %w", err)
	}

	request.Header.Set("User-Agent", userAgent)

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", sourceURL, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %s", sourceURL,
			response.Status)
	}

	document, err := html.Parse(response.Body)
	if err != nil {
		return nil, fmt.Errorf("parse Wikipedia page: %w", err)
	}

	seen := make(map[string]struct{})
	collectConstituents(document, seen)

	if len(seen) < 450 || len(seen) > 550 {
		return nil, fmt.Errorf(
			"wikipedia constituent count %d is outside expected range",
			len(seen),
		)
	}

	tickers := make([]string, 0, len(seen))
	for ticker := range seen {
		tickers = append(tickers, ticker)
	}

	sort.Strings(tickers)

	return tickers, nil
}

func collectConstituents(node *html.Node, seen map[string]struct{}) {
	if node.Type == html.ElementNode && node.Data == "table" &&
		hasID(node, "constituents") {
		collectTableTickers(node, seen)
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		collectConstituents(child, seen)
	}
}

func collectTableTickers(table *html.Node, seen map[string]struct{}) {
	if table.Type == html.ElementNode && table.Data == "tr" {
		value := constituents.NormalizeTicker(firstAnchorText(table))
		if tickerPattern.MatchString(value) {
			seen[value] = struct{}{}
		}

		return
	}

	for child := table.FirstChild; child != nil; child = child.NextSibling {
		collectTableTickers(child, seen)
	}
}

func firstAnchorText(node *html.Node) string {
	if node.Type == html.ElementNode && node.Data == "a" {
		return textContent(node)
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if value := firstAnchorText(child); value != "" {
			return value
		}
	}

	return ""
}

func textContent(node *html.Node) string {
	if node.Type == html.TextNode {
		return node.Data
	}

	var parts []string
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		parts = append(parts, textContent(child))
	}

	return strings.Join(parts, "")
}

func hasID(node *html.Node, id string) bool {
	for _, attribute := range node.Attr {
		if attribute.Key == "id" && attribute.Val == id {
			return true
		}
	}

	return false
}
