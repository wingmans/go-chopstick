// Package wikipedia reads the temporary Wikipedia constituent source.
package wikipedia

import (
	"context"
	"errors"
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

// FetchMembership returns the membership identities published in the source
// table. The caller remains responsible for verifying them against the SEC.
func FetchMembership(ctx context.Context, client *http.Client, sourceURL,
	userAgent string,
) ([]constituents.Member, error) {
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

	members, err := collectConstituents(document)
	if err != nil {
		return nil, err
	}

	if len(members) < 450 || len(members) > 550 {
		return nil, fmt.Errorf(
			"wikipedia constituent count %d is outside expected range",
			len(members),
		)
	}

	sort.Slice(members, func(left, right int) bool {
		return members[left].Ticker < members[right].Ticker
	})

	return members, nil
}

func collectConstituents(document *html.Node) ([]constituents.Member, error) {
	table := constituentTable(document)
	if table == nil {
		return nil, errors.New("wikipedia constituent table was not found")
	}

	rows := tableRows(table)

	columns, err := constituentColumns(rows)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]constituents.Member)

	for _, row := range rows {
		cells := rowCells(row, "td")
		if len(cells) == 0 {
			continue
		}

		maximum := max(columns.symbol, columns.security, columns.cik)
		if maximum >= len(cells) {
			return nil, errors.New(
				"wikipedia constituent row has fewer cells than its header",
			)
		}

		ticker := constituents.NormalizeTicker(textContent(cells[columns.symbol]))
		if !tickerPattern.MatchString(ticker) {
			return nil, fmt.Errorf("invalid Wikipedia ticker %q", ticker)
		}

		cik, err := constituents.NormalizeCIK(textContent(cells[columns.cik]))
		if err != nil {
			return nil, fmt.Errorf("wikipedia ticker %s: %w", ticker, err)
		}

		member := constituents.Member{
			CIK: cik, Ticker: ticker,
			Name: strings.TrimSpace(textContent(cells[columns.security])),
		}
		if member.Name == "" {
			return nil, fmt.Errorf("wikipedia ticker %s has no name", ticker)
		}

		if previous, exists := seen[ticker]; exists {
			return nil, fmt.Errorf(
				"duplicate Wikipedia ticker %s for CIKs %s and %s",
				ticker, previous.CIK, cik,
			)
		}

		seen[ticker] = member
	}

	members := make([]constituents.Member, 0, len(seen))
	for _, member := range seen {
		members = append(members, member)
	}

	return members, nil
}

func constituentTable(node *html.Node) *html.Node {
	if node.Type == html.ElementNode && node.Data == "table" &&
		hasID(node, "constituents") {
		return node
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if table := constituentTable(child); table != nil {
			return table
		}
	}

	return nil
}

func tableRows(node *html.Node) []*html.Node {
	if node.Type == html.ElementNode && node.Data == "tr" {
		return []*html.Node{node}
	}

	var rows []*html.Node
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		rows = append(rows, tableRows(child)...)
	}

	return rows
}

type tableColumns struct {
	symbol   int
	security int
	cik      int
}

func constituentColumns(rows []*html.Node) (tableColumns, error) {
	for _, row := range rows {
		headers := rowCells(row, "th")
		if len(headers) == 0 {
			continue
		}

		columns := tableColumns{symbol: -1, security: -1, cik: -1}

		for index, header := range headers {
			switch strings.ToLower(strings.TrimSpace(textContent(header))) {
			case "symbol":
				columns.symbol = index
			case "security":
				columns.security = index
			case "cik":
				columns.cik = index
			}
		}

		if columns.symbol >= 0 && columns.security >= 0 && columns.cik >= 0 {
			return columns, nil
		}
	}

	return tableColumns{}, errors.New(
		"wikipedia constituent table is missing Symbol, Security, or CIK",
	)
}

func rowCells(row *html.Node, element string) []*html.Node {
	var cells []*html.Node

	for child := row.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == element {
			cells = append(cells, child)
		}
	}

	return cells
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
