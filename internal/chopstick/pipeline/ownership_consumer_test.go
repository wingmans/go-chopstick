package pipeline

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"wingman.com/chopstick/internal/edgar"
	ownershipimporter "wingman.com/chopstick/internal/ownership/importer"
	ownershipstore "wingman.com/chopstick/internal/ownership/store"
)

type noNetworkTransport struct {
	called bool
}

func (transport *noNetworkTransport) RoundTrip(
	*http.Request,
) (*http.Response, error) {
	transport.called = true

	return nil, errors.New("unexpected network request")
}

func TestProcessOwnershipEventUsesConfiguredCache(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cache := filepath.Join(root, "filings")
	accession := "0000000001-26-000001"
	relative := "edgar/data/100/000000000126000001/" + accession + ".txt"
	writeOwnershipCacheFile(t, cache, relative, accession)

	store, err := ownershipstore.Open(t.Context(),
		filepath.Join(root, "ownership.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	transport := &noNetworkTransport{}

	downloader, err := edgar.NewFilingDownloader(
		&http.Client{Transport: transport}, edgar.FilingDownloadConfig{
			Directory: cache, UserAgent: "ownership-test test@example.com",
		})
	if err != nil {
		t.Fatal(err)
	}

	decision, err := processOwnershipEvent(t.Context(), store, downloader,
		cache, Event{
			AccessionNumber: accession, CIK: "100", CompanyName: "Issuer",
			FormType: "4", FilingDate: "2026-02-15", FilingPath: relative,
		})
	if err != nil {
		t.Fatal(err)
	}

	if decision != ownershipimporter.DecisionProcessed {
		t.Fatalf("decision = %s", decision.String())
	}

	if transport.called {
		t.Fatal("cached ownership filing made a network request")
	}

	status, err := store.Status(t.Context(), accession)
	if err != nil {
		t.Fatal(err)
	}

	if status.Status != "processed" {
		t.Fatalf("ownership status = %+v", status)
	}
}

func writeOwnershipCacheFile(t *testing.T, cache, relative,
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

	submission := fmt.Sprintf(`<SEC-DOCUMENT>%s.txt
<SEC-HEADER>header
ACCESSION NUMBER: %s
CONFORMED SUBMISSION TYPE: 4
CENTRAL INDEX KEY: 0000000100
FILED AS OF DATE: 20260215
CONFORMED PERIOD OF REPORT: 20260210
PUBLIC DOCUMENT COUNT: 1
</SEC-HEADER>
<DOCUMENT>
<TYPE>4
<SEQUENCE>1
<FILENAME>form4.xml
<TEXT>
<XML>
%s
</XML>
</TEXT>
</DOCUMENT>
</SEC-DOCUMENT>
`, accession, accession, xml)

	path, err := edgar.LocalFilingPath(cache, relative)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(submission), 0o600); err != nil {
		t.Fatal(err)
	}
}
