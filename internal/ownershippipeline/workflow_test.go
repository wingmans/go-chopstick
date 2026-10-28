package ownershippipeline

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/ownershipstore"
)

type rejectingTransport struct {
	called bool
}

func (transport *rejectingTransport) RoundTrip(
	*http.Request,
) (*http.Response, error) {
	transport.called = true

	return nil, errors.New("unexpected network request")
}

func TestRunReusesCacheDeduplicatesAndResumes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	database := filepath.Join(root, "ownership.db")
	master := filepath.Join(root, "master.tsv")
	accession := "0000000001-26-000001"
	relative := "edgar/data/100/" + accession + ".txt"
	writeCachedSubmission(t, cache, relative, accession)
	writeMaster(t, master,
		"100|Issuer|4|2026-02-15|"+relative,
		"200|Owner|4|2026-02-15|"+relative,
		"300|Legacy Owner|SC 13G|2023-01-02|"+
			"edgar/data/300/0000000003-23-000001.txt")

	transport := &rejectingTransport{}
	client := &http.Client{Transport: transport}
	config := Config{
		MasterPath: master, DatabasePath: database,
		Download: edgar.FilingDownloadConfig{
			Directory: cache, UserAgent: "ownership-test test@example.com",
		},
		FromYear: 2022, ToYear: 2026,
	}

	summary, err := Run(t.Context(), client, config)
	if err != nil {
		t.Fatal(err)
	}

	if summary.Selected != 3 || summary.Processed != 1 ||
		summary.Reused != 1 || summary.Unsupported != 1 ||
		summary.Failed != 0 {
		t.Fatalf("unexpected first summary: %+v", summary)
	}

	if transport.called {
		t.Fatal("cached import made a network request")
	}

	summary, err = Run(t.Context(), client, config)
	if err != nil {
		t.Fatal(err)
	}

	if summary.Processed != 0 || summary.Reused != 3 ||
		summary.Unsupported != 0 {
		t.Fatalf("unexpected resumed summary: %+v", summary)
	}

	store, err := ownershipstore.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	count, err := store.RowCount(t.Context(), "ownership_filings")
	if err != nil {
		t.Fatal(err)
	}

	if count != 2 {
		t.Fatalf("ownership filing count = %d, want 2", count)
	}

	coverage, err := store.LatestCoverage(t.Context(), "1")
	if err != nil {
		t.Fatal(err)
	}

	if len(coverage) != 3 {
		t.Fatalf("coverage family count = %d, want 3", len(coverage))
	}
}

func TestNoopPlansUniqueAccessionsWithoutWritingDatabase(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	master := filepath.Join(root, "master.tsv")
	database := filepath.Join(root, "ownership.db")
	relative := "edgar/data/100/0000000001-26-000001.txt"
	writeMaster(t, master,
		"100|Issuer|SCHEDULE 13G|2026-02-15|"+relative,
		"200|Owner|SCHEDULE 13G|2026-02-15|"+relative)

	summary, err := Run(t.Context(), nil, Config{
		MasterPath: master, DatabasePath: database,
		FromYear: 2022, Noop: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if summary.Selected != 2 || summary.Planned != 1 {
		t.Fatalf("unexpected plan summary: %+v", summary)
	}

	if _, err := os.Stat(database); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("noop database stat error = %v", err)
	}
}

func writeCachedSubmission(t *testing.T, cache, relative,
	accession string,
) {
	t.Helper()

	xml := `<ownershipDocument>
<documentType>4</documentType><periodOfReport>2026-02-10</periodOfReport>
<issuer><issuerCik>0000000100</issuerCik><issuerName>Example Inc</issuerName>
<issuerTradingSymbol>EXM</issuerTradingSymbol></issuer>
<reportingOwner><reportingOwnerId><rptOwnerCik>0000000200</rptOwnerCik>
<rptOwnerName>Owner One</rptOwnerName></reportingOwnerId>
<reportingOwnerRelationship><isDirector>1</isDirector>
</reportingOwnerRelationship></reportingOwner>
<nonDerivativeTable><nonDerivativeTransaction>
<securityTitle><value>Common Stock</value></securityTitle>
<transactionDate><value>2026-02-09</value></transactionDate>
<transactionCoding><transactionFormType>4</transactionFormType>
<transactionCode>S</transactionCode></transactionCoding>
<transactionAmounts><transactionShares><value>10</value></transactionShares>
<transactionAcquiredDisposedCode><value>D</value>
</transactionAcquiredDisposedCode></transactionAmounts>
<postTransactionAmounts><sharesOwnedFollowingTransaction><value>90</value>
</sharesOwnedFollowingTransaction></postTransactionAmounts>
<ownershipNature><directOrIndirectOwnership><value>D</value>
</directOrIndirectOwnership></ownershipNature>
</nonDerivativeTransaction></nonDerivativeTable></ownershipDocument>`

	var submission strings.Builder
	fmt.Fprintf(&submission, "<SEC-DOCUMENT>%s.txt\n", accession)
	fmt.Fprintln(&submission, "<SEC-HEADER>header")
	fmt.Fprintf(&submission, "ACCESSION NUMBER: %s\n", accession)
	fmt.Fprintln(&submission, "CONFORMED SUBMISSION TYPE: 4")
	fmt.Fprintln(&submission, "CENTRAL INDEX KEY: 0000000100")
	fmt.Fprintln(&submission, "FILED AS OF DATE: 20260215")
	fmt.Fprintln(&submission, "CONFORMED PERIOD OF REPORT: 20260210")
	fmt.Fprintln(&submission, "PUBLIC DOCUMENT COUNT: 1")
	fmt.Fprintln(&submission, "</SEC-HEADER>")
	fmt.Fprintln(&submission, "<DOCUMENT>")
	fmt.Fprintln(&submission, "<TYPE>4")
	fmt.Fprintln(&submission, "<SEQUENCE>1")
	fmt.Fprintln(&submission, "<FILENAME>form4.xml")
	fmt.Fprintln(&submission, "<TEXT>")
	fmt.Fprintln(&submission, "<XML>")
	fmt.Fprintln(&submission, xml)
	fmt.Fprintln(&submission, "</XML>")
	fmt.Fprintln(&submission, "</TEXT>")
	fmt.Fprintln(&submission, "</DOCUMENT>")
	fmt.Fprintln(&submission, "</SEC-DOCUMENT>")

	path, err := edgar.LocalFilingPath(cache, relative)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(submission.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeMaster(t *testing.T, path string, rows ...string) {
	t.Helper()

	if err := os.WriteFile(path,
		[]byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
