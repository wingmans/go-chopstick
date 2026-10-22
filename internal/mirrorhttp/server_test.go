package mirrorhttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wingman.com/fetch-ecb/internal/mirror"
)

func TestServerSearchAndContentUseAPI(t *testing.T) {
	api := &fakeAPI{}
	server := New(api)
	search := httptest.NewRecorder()
	server.ServeHTTP(search, httptest.NewRequestWithContext(t.Context(),
		http.MethodGet,
		"/v1/submissions?form=10-Q&filing_year=2025&limit=20", nil))

	if search.Code != http.StatusOK || api.filter.FormType != "10-Q" ||
		api.filter.FilingYear != 2025 || api.filter.Limit != 20 {
		t.Fatalf("search status=%d filter=%+v body=%s", search.Code,
			api.filter, search.Body.String())
	}

	content := httptest.NewRecorder()
	server.ServeHTTP(content, httptest.NewRequestWithContext(t.Context(),
		http.MethodGet,
		"/v1/submissions/accession/content", nil))

	if content.Code != http.StatusOK || content.Body.String() != "raw" ||
		api.accession != "accession" {
		t.Fatalf("content status=%d accession=%q body=%q", content.Code,
			api.accession, content.Body.String())
	}
}

func TestServerMapsDomainErrors(t *testing.T) {
	server := New(&fakeAPI{getErr: mirror.ErrNotFound})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(),
		http.MethodGet,
		"/v1/submissions/missing", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
}

type fakeAPI struct {
	filter    mirror.SearchFilter
	accession string
	getErr    error
}

func (a *fakeAPI) Ensure(_ context.Context, ref mirror.SubmissionRef) (
	mirror.Submission, error,
) {
	return mirror.Submission{SubmissionRef: ref}, nil
}

func (a *fakeAPI) Get(_ context.Context, accession string) (
	mirror.Submission, error,
) {
	a.accession = accession

	return mirror.Submission{}, a.getErr
}

func (a *fakeAPI) Search(_ context.Context, filter mirror.SearchFilter) (
	mirror.SubmissionPage, error,
) {
	a.filter = filter

	return mirror.SubmissionPage{Items: []mirror.Submission{}}, nil
}

func (a *fakeAPI) Open(_ context.Context, accession string) (
	mirror.Content, error,
) {
	a.accession = accession

	return mirror.Content{
		Submission: mirror.Submission{LogicalBytes: 3},
		Reader:     io.NopCloser(strings.NewReader("raw")),
	}, nil
}

func (a *fakeAPI) Stats(_ context.Context) (mirror.Stats, error) {
	return mirror.Stats{}, nil
}
