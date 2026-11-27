package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	uitest "github.com/wingmans/htmx-linter"
)

func TestUISources(t *testing.T) {
	const envVar = "CHOPSTICK_UI_WARNINGS_AS_ERRORS"

	value := os.Getenv(envVar)
	if value == "" {
		value = "false"
	}

	strict, err := strconv.ParseBool(value)
	if err != nil {
		t.Fatalf("invalid %s: %v", envVar, err)
	}

	uitest.ReportSourceRules(t, ".", uitest.DefaultLintConfig(), strict)
}

func TestServerRendersHTMX(t *testing.T) {
	server := newCIKTestServer(t)
	cfg := uitest.DefaultLintConfig()
	fullPage := uitest.NewHarness(t, server).GetPage("/?q=MSFT").AssertStatus(http.StatusOK).Lint(cfg)

	for _, test := range []struct {
		name, target string
		fullPage     bool
	}{
		{"dashboard", "div#dashboard", false},
		{"body", "body", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/?q=MSFT", nil)
			request.Header.Set("Hx-Request", "true")
			request.Header.Set("Hx-Target", test.target)
			server.ServeHTTP(record, request)

			page := uitest.ParsePage(t, record.Code, record.Header(), record.Body.String()).Lint(cfg)

			if test.fullPage {
				requireResponse(t, record, "<!doctype html>", "class=\"dashboard-page\"")

				return
			}

			if record.Code != http.StatusOK || strings.Contains(record.Body.String(), "<!doctype html>") {
				t.Fatalf("unexpected dashboard fragment: status=%d body=%s", record.Code, record.Body.String())
			}

			requireResponse(t, record, "id=\"dashboard\"", "0001193125-26-027207")
			page.AssertOOBTargetsExistOnFullPage(fullPage)
		})
	}
}
