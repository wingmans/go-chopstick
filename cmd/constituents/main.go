// Package main builds current constituent-set snapshots.
//
//nolint:wsl_v5 // This command keeps its small workflow readable as a unit.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
	"wingman.com/fetch-ecb/internal/constituents"
)

const (
	defaultWikipediaURL = "https://en.wikipedia.org/wiki/List_of_S%26P_500_companies"
	defaultTickerURL    = "https://www.sec.gov/files/company_tickers.json"
	defaultOutput       = "./data/sets/sp500.json"
	userAgent           = "wingman paul@wingmen.io"
)

type secCompany struct {
	CIK    json.Number `json:"cik_str"`
	Ticker string      `json:"ticker"`
	Title  string      `json:"title"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}

		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	var output, wikipediaURL, tickerURL string
	flags := flag.NewFlagSet("constituents", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		fmt.Fprintln(os.Stdout, "Usage: constituents [options]")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Build a current constituent-set JSON snapshot.")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Options:")
		fmt.Fprintln(os.Stdout, "  -o, --output <path>       Output JSON path.")
		fmt.Fprintln(os.Stdout, "  -w, --wikipedia-url <url> Current Wikipedia constituent page.")
		fmt.Fprintln(os.Stdout, "  -t, --ticker-url <url>    SEC ticker/CIK JSON URL.")
	}
	flags.StringVar(&output, "o", defaultOutput, "output JSON path")
	flags.StringVar(&output, "output", defaultOutput, "output JSON path")
	flags.StringVar(&wikipediaURL, "w", defaultWikipediaURL, "current Wikipedia constituent page")
	flags.StringVar(&wikipediaURL, "wikipedia-url", defaultWikipediaURL, "current Wikipedia constituent page")
	flags.StringVar(&tickerURL, "t", defaultTickerURL, "SEC ticker/CIK JSON URL")
	flags.StringVar(&tickerURL, "ticker-url", defaultTickerURL, "SEC ticker/CIK JSON URL")
	if err := flags.Parse(args); err != nil {
		return err
	}

	client := &http.Client{
		Transport:     nil,
		CheckRedirect: nil,
		Jar:           nil,
		Timeout:       30 * time.Second,
	}
	ctx := context.Background()
	tickers, err := fetchSECCompanies(ctx, client, tickerURL)
	if err != nil {
		return err
	}
	memberTickers, err := fetchWikipediaTickers(ctx, client, wikipediaURL)
	if err != nil {
		return err
	}

	set := constituents.Set{
		Name:    "sp500",
		AsOf:    time.Now().UTC().Format("2006-01-02"),
		Source:  wikipediaURL,
		Members: make([]constituents.Member, 0, len(memberTickers)),
	}
	for _, ticker := range memberTickers {
		if ticker == "SYMBOL" {
			continue
		}
		company, ok := tickers[normalizeTicker(ticker)]
		if !ok {
			return fmt.Errorf("ticker %s was not found in SEC company ticker data", ticker)
		}
		cik, err := constituents.NormalizeCIK(company.CIK.String())
		if err != nil {
			return fmt.Errorf("ticker %s: %w", ticker, err)
		}
		set.Members = append(set.Members, constituents.Member{
			CIK: cik, Ticker: normalizeTicker(ticker), Name: company.Title,
		})
	}

	if err := os.MkdirAll(filepathDir(output), 0o750); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	file, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer func() { _ = file.Close() }()
	if err := set.WriteJSON(file); err != nil {
		return err
	}
	fmt.Printf("wrote %d current constituents to %s\n", len(set.Members), output)

	return nil
}

func fetchSECCompanies(ctx context.Context, client *http.Client, url string) (map[string]secCompany, error) {
	body, err := fetch(ctx, client, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	var raw map[string]secCompany
	if err := json.NewDecoder(body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode SEC ticker data: %w", err)
	}
	companies := make(map[string]secCompany, len(raw))
	for _, company := range raw {
		companies[normalizeTicker(company.Ticker)] = company
	}

	return companies, nil
}

var tickerPattern = regexp.MustCompile(`^[A-Z][A-Z0-9.-]*$`)

func fetchWikipediaTickers(ctx context.Context, client *http.Client, url string) ([]string, error) {
	body, err := fetch(ctx, client, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	doc, err := html.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("parse Wikipedia page: %w", err)
	}
	seen := make(map[string]struct{})
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "table" && hasID(node, "constituents") {
			collectTableTickers(node, seen)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if len(seen) < 450 || len(seen) > 550 {
		return nil, fmt.Errorf("wikipedia constituent count %d is outside expected range", len(seen))
	}
	tickers := make([]string, 0, len(seen))
	for ticker := range seen {
		tickers = append(tickers, ticker)
	}
	sort.Strings(tickers)

	return tickers, nil
}

func collectTableTickers(table *html.Node, seen map[string]struct{}) {
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "tr" {
			value := normalizeTicker(firstAnchorText(node))
			if tickerPattern.MatchString(value) {
				seen[value] = struct{}{}
			}

			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(table)
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

func fetch(ctx context.Context, client *http.Client, url string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("User-Agent", userAgent)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()

		return nil, fmt.Errorf("fetch %s: HTTP %s", url, response.Status)
	}

	return response.Body, nil
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

func normalizeTicker(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "\u00a0", "")
	value = strings.ReplaceAll(value, " ", "")

	return strings.ReplaceAll(value, ".", "-")
}

func hasID(node *html.Node, id string) bool {
	for _, attribute := range node.Attr {
		if attribute.Key == "id" && attribute.Val == id {
			return true
		}
	}

	return false
}

func filepathDir(path string) string {
	index := strings.LastIndexAny(path, "/\\")
	if index < 0 {
		return "."
	}
	if index == 0 {
		return path[:1]
	}

	return path[:index]
}
