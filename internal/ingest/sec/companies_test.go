package sec

import (
	"context"
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

func TestFetchCompaniesIndexesNormalizedTickers(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			if got := request.Header.Get("User-Agent"); got != "test-agent" {
				t.Fatalf("User-Agent = %q", got)
			}

			body := `{"0":{"cik_str":1067983,"ticker":"BRK-B",` +
				`"title":"Berkshire Hathaway Inc"}}`

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		},
	)}

	companies, err := FetchCompanies(
		context.Background(), client, "https://sec.test/companies", "test-agent",
	)
	if err != nil {
		t.Fatal(err)
	}

	company, ok := companies["BRK-B"]
	if !ok {
		t.Fatal("normalized ticker BRK-B is missing")
	}

	if company.CIK.String() != "1067983" {
		t.Fatalf("CIK = %q", company.CIK.String())
	}
}
