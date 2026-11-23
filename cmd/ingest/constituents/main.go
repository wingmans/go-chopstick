// Package main builds current constituent-set of sp500 companies defined in wikipedia page
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"wingman.com/chopstick/internal/platform/appdata"
	"wingman.com/chopstick/internal/constituents"
	"wingman.com/chopstick/internal/ingest/sec"
	"wingman.com/chopstick/internal/ingest/wikipedia"
	"wingman.com/chopstick/internal/platform/outbound"
)

const (
	defaultWikipediaURL = wikipedia.DefaultURL
	defaultTickerURL    = sec.DefaultCompanyTickersURL
	userAgent           = "wingman paul@wingmen.io"
)

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

	defaultOutput := defaultConstituentOutput()

	flags := flag.NewFlagSet("constituents", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		fmt.Fprintln(os.Stdout, "Usage: constituents [options]")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Build a current constituent-set JSON snapshot.")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Options:")
		fmt.Fprintln(os.Stdout, "  -o, --output <path>       Output JSON path.")
		fmt.Fprintf(os.Stdout, "                             Default: %s.\n", defaultOutput)
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

	client := outbound.NewClient(30 * time.Second)
	ctx := context.Background()

	tickers, err := sec.FetchCompanies(ctx, client, tickerURL, userAgent)
	if err != nil {
		return err
	}

	memberTickers, err := wikipedia.FetchMembership(
		ctx, client, wikipediaURL, userAgent,
	)
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

		company, ok := tickers[constituents.NormalizeTicker(ticker)]
		if !ok {
			return fmt.Errorf("ticker %s was not found in SEC company ticker data", ticker)
		}

		cik, err := constituents.NormalizeCIK(company.CIK.String())
		if err != nil {
			return fmt.Errorf("ticker %s: %w", ticker, err)
		}

		set.Members = append(set.Members, constituents.Member{
			CIK: cik, Ticker: constituents.NormalizeTicker(ticker),
			Name: company.Title,
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

func defaultConstituentOutput() string {
	return appdata.Path("sets", "sp500.json")
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
