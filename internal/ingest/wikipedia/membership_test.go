package wikipedia

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}

func TestFetchMembershipReadsConstituentTable(t *testing.T) {
	t.Parallel()

	var page strings.Builder
	page.WriteString(`<html><table id="other"><tr><td><a>IGNORE</a></td></tr>`)
	page.WriteString(`</table><table id="constituents">`)

	for index := range 449 {
		_, _ = fmt.Fprintf(&page, "<tr><td><a>T%03d</a></td></tr>", index)
	}

	page.WriteString(`<tr><td><a>BRK-B</a></td></tr></table></html>`)

	client := &http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			if got := request.Header.Get("User-Agent"); got != "test-agent" {
				t.Fatalf("User-Agent = %q", got)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(page.String())),
				Header:     make(http.Header),
			}, nil
		},
	)}

	tickers, err := FetchMembership(
		context.Background(), client, "https://wikipedia.test/list", "test-agent",
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(tickers) != 450 {
		t.Fatalf("ticker count = %d", len(tickers))
	}

	if tickers[0] != "BRK-B" {
		t.Fatalf("first ticker = %q", tickers[0])
	}
}
