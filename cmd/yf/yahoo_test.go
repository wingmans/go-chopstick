package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestYahooClientFetch(t *testing.T) {
	from := mustDate(t, "1986-03-13")
	to := mustDate(t, "1986-03-14")
	timestamps := []int64{
		from.Add(14 * time.Hour).Unix(),
		to.Add(14 * time.Hour).Unix(),
	}

	var requestedPath string

	var requestedQuery url.Values

	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter, request *http.Request,
	) {
		requestedPath = request.URL.Path
		requestedQuery = request.URL.Query()

		response.Header().Set("Content-Type", "application/json")
		fmt.Fprint(response, yahooFixture(timestamps))
	}))
	defer server.Close()

	client := testYahooClient(server)

	result, err := client.fetch(context.Background(), "MSFT", from, to)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if requestedPath != "/MSFT" {
		t.Fatalf("request path = %q, want /MSFT", requestedPath)
	}

	wantPeriod2 := to.AddDate(0, 0, 1).Unix()
	if requestedQuery.Get("period2") != strconv.FormatInt(wantPeriod2, 10) {
		t.Fatalf(
			"period2 = %q, want %d",
			requestedQuery.Get("period2"), wantPeriod2,
		)
	}

	if len(result.Prices) != 2 {
		t.Fatalf("price count = %d, want 2", len(result.Prices))
	}

	first := result.Prices[0]
	if first.Open != 25 || first.Close != 26 || first.AdjClose != 0.06 {
		t.Fatalf("first price = %+v", first)
	}
}

func TestYahooClientRetriesThroughPacer(t *testing.T) {
	from := mustDate(t, "2024-01-02")
	to := from

	var mu sync.Mutex

	requestTimes := make([]time.Time, 0, 2)

	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter, _ *http.Request,
	) {
		mu.Lock()

		requestTimes = append(requestTimes, time.Now())
		requestCount := len(requestTimes)
		mu.Unlock()

		if requestCount == 1 {
			http.Error(response, "try later", http.StatusTooManyRequests)

			return
		}

		fmt.Fprint(response, yahooFixture([]int64{
			from.Add(14 * time.Hour).Unix(),
		}))
	}))
	defer server.Close()

	client := testYahooClient(server)
	client.maxAttempts = 2
	client.pacer = newPacer(20 * time.Millisecond)

	if _, err := client.fetch(context.Background(), "MSFT", from, to); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(requestTimes) != 2 {
		t.Fatalf("request count = %d, want 2", len(requestTimes))
	}

	if elapsed := requestTimes[1].Sub(requestTimes[0]); elapsed < 18*time.Millisecond {
		t.Fatalf("requests were only %s apart", elapsed)
	}
}

func TestParseYahooResponseRejectsDuplicateDates(t *testing.T) {
	from := mustDate(t, "2024-01-02")
	raw := []byte(yahooFixture([]int64{
		from.Add(14 * time.Hour).Unix(),
		from.Add(15 * time.Hour).Unix(),
	}))

	_, err := parseYahooResponse("MSFT", raw, from, from)
	if err == nil {
		t.Fatal("expected duplicate-date error")
	}
}

func TestParseYahooResponseIgnoresLeadingOverlap(t *testing.T) {
	from := mustDate(t, "2024-01-03")
	previous := from.AddDate(0, 0, -1)
	raw := []byte(yahooFixture([]int64{
		previous.Add(14 * time.Hour).Unix(),
		from.Add(14 * time.Hour).Unix(),
	}))

	result, err := parseYahooResponse("MSFT", raw, from, from)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}

	if len(result.Prices) != 1 ||
		result.Prices[0].Date.Format(dateLayout) != "2024-01-03" {
		t.Fatalf("prices = %+v, want only 2024-01-03", result.Prices)
	}
}

func TestParseYahooResponseIncludesCorporateActions(t *testing.T) {
	from := mustDate(t, "2024-01-02")
	to := mustDate(t, "2024-01-03")
	raw := yahooFixture([]int64{from.Add(14 * time.Hour).Unix()})
	events := fmt.Sprintf(`"events": {
        "dividends": {%q: {"amount": 0.75, "date": %d}},
        "splits": {%q: {
          "date": %d, "numerator": 2, "denominator": 1
        }}
      },
      "indicators":`,
		"dividend-event", from.Add(14*time.Hour).Unix(),
		"split-event", to.Add(14*time.Hour).Unix(),
	)
	raw = strings.Replace(raw, `"indicators":`, events, 1)

	result, err := parseYahooResponse("MSFT", []byte(raw), from, to)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}

	if len(result.Actions) != 2 {
		t.Fatalf("actions = %+v, want dividend and split", result.Actions)
	}

	if result.Actions[0].Type != "dividend" || result.Actions[0].Amount != 0.75 {
		t.Fatalf("dividend = %+v", result.Actions[0])
	}

	if result.Actions[1].Type != "split" ||
		result.Actions[1].Numerator != 2 ||
		result.Actions[1].Denominator != 1 {
		t.Fatalf("split = %+v", result.Actions[1])
	}
}

func TestParseYahooResponseRejectsInvalidData(t *testing.T) {
	from := mustDate(t, "2024-01-02")
	valid := yahooFixture([]int64{from.Add(14 * time.Hour).Unix()})
	tests := []struct {
		name string
		raw  string
	}{
		{name: "malformed JSON", raw: "{"},
		{
			name: "wrong currency",
			raw:  strings.Replace(valid, `"currency": "USD"`, `"currency": "EUR"`, 1),
		},
		{
			name: "null open",
			raw:  strings.Replace(valid, `"open": [25]`, `"open": [null]`, 1),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseYahooResponse(
				"MSFT", []byte(test.raw), from, from,
			); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func testYahooClient(server *httptest.Server) *yahooClient {
	client := newYahooClient(
		server.Client(), server.URL, slog.New(slog.DiscardHandler),
	)
	client.maxAttempts = 1
	client.pacer = newPacer(0)

	return client
}

func yahooFixture(timestamps []int64) string {
	opens := make([]string, len(timestamps))
	closes := make([]string, len(timestamps))
	adjusted := make([]string, len(timestamps))
	formattedTimestamps := make([]string, len(timestamps))

	for index, timestamp := range timestamps {
		opens[index] = strconv.Itoa(25 + index)
		closes[index] = strconv.Itoa(26 + index)
		adjusted[index] = fmt.Sprintf("0.%02d", 6+index)
		formattedTimestamps[index] = strconv.FormatInt(timestamp, 10)
	}

	return fmt.Sprintf(`{
  "chart": {
    "result": [{
      "meta": {"currency": "USD", "symbol": %q},
      "timestamp": [%s],
      "indicators": {
        "quote": [{"open": [%s], "close": [%s]}],
        "adjclose": [{"adjclose": [%s]}]
      }
    }],
    "error": null
  }
}`,
		"MSFT",
		joinValues(formattedTimestamps),
		joinValues(opens),
		joinValues(closes),
		joinValues(adjusted),
	)
}

func joinValues(values []string) string {
	return strings.Join(values, ",")
}

func mustDate(t *testing.T, value string) time.Time {
	t.Helper()

	date, err := time.Parse(dateLayout, value)
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}

	return date
}
