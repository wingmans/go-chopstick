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
	page.WriteString(`</table><table id="constituents"><tr>`)
	page.WriteString(`<th>Symbol</th><th>Security</th><th>Sector</th>`)
	page.WriteString(`<th>Industry</th><th>Headquarters</th><th>Added</th>`)
	page.WriteString(`<th>CIK</th></tr>`)

	for index := range 449 {
		_, _ = fmt.Fprintf(&page, `<tr><td><a>T%03d</a></td>`+
			`<td>Test Company %03d</td><td></td><td></td><td></td>`+
			`<td></td><td>%010d</td></tr>`, index, index, index+1)
	}

	page.WriteString(`<tr><td><a>BRK.B</a></td><td>Berkshire Hathaway</td>`)
	page.WriteString(`<td></td><td></td><td></td><td></td>`)
	page.WriteString(`<td>0001067983</td></tr></table></html>`)

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

	members, err := FetchMembership(
		context.Background(), client, "https://wikipedia.test/list", "test-agent",
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(members) != 450 {
		t.Fatalf("member count = %d", len(members))
	}

	if members[0].Ticker != "BRK-B" || members[0].CIK != "0001067983" ||
		members[0].Name != "Berkshire Hathaway" {
		t.Fatalf("first member = %+v", members[0])
	}
}
