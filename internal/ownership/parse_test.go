package ownership

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testDocument struct {
	documentType string
	filename     string
	content      string
}

func writeOwnershipSubmission(t *testing.T, form, accession string,
	documents ...testDocument,
) string {
	t.Helper()

	var source strings.Builder
	fmt.Fprintf(&source, "<SEC-DOCUMENT>%s.txt\n", accession)
	fmt.Fprintln(&source, "<SEC-HEADER>header")
	fmt.Fprintf(&source, "ACCESSION NUMBER: %s\n", accession)
	fmt.Fprintf(&source, "CONFORMED SUBMISSION TYPE: %s\n", form)
	fmt.Fprintln(&source, "CENTRAL INDEX KEY: 0000000001")
	fmt.Fprintln(&source, "FILED AS OF DATE: 20260215")
	fmt.Fprintln(&source, "CONFORMED PERIOD OF REPORT: 20251231")
	fmt.Fprintf(&source, "PUBLIC DOCUMENT COUNT: %d\n", len(documents))
	fmt.Fprintln(&source, "</SEC-HEADER>")

	for index, document := range documents {
		fmt.Fprintln(&source, "<DOCUMENT>")
		fmt.Fprintf(&source, "<TYPE>%s\n", document.documentType)
		fmt.Fprintf(&source, "<SEQUENCE>%d\n", index+1)
		fmt.Fprintf(&source, "<FILENAME>%s\n", document.filename)
		fmt.Fprintln(&source, "<TEXT>")
		fmt.Fprintln(&source, "<XML>")
		fmt.Fprintln(&source, document.content)
		fmt.Fprintln(&source, "</XML>")
		fmt.Fprintln(&source, "</TEXT>")
		fmt.Fprintln(&source, "</DOCUMENT>")
	}

	fmt.Fprintln(&source, "</SEC-DOCUMENT>")

	path := filepath.Join(t.TempDir(), accession+".txt")
	if err := os.WriteFile(path, []byte(source.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestParseSection16(t *testing.T) {
	t.Parallel()

	xml := `<ownershipDocument>
<documentType>4</documentType><periodOfReport>2026-02-10</periodOfReport>
<issuer><issuerCik>0000000100</issuerCik><issuerName>Example Inc</issuerName>
<issuerTradingSymbol>EXM</issuerTradingSymbol></issuer>
<reportingOwner><reportingOwnerId><rptOwnerCik>0000000200</rptOwnerCik>
<rptOwnerName>Owner One</rptOwnerName></reportingOwnerId>
<reportingOwnerRelationship><isDirector>1</isDirector><isOfficer>0</isOfficer>
</reportingOwnerRelationship></reportingOwner>
<nonDerivativeTable><nonDerivativeTransaction>
<securityTitle><value>Common Stock</value></securityTitle>
<transactionDate><value>2026-02-09</value></transactionDate>
<transactionCoding><transactionFormType>4</transactionFormType>
<transactionCode>S</transactionCode></transactionCoding>
<transactionAmounts><transactionShares><value>10</value></transactionShares>
<transactionPricePerShare><value>25.50</value></transactionPricePerShare>
<transactionAcquiredDisposedCode><value>D</value></transactionAcquiredDisposedCode>
</transactionAmounts><postTransactionAmounts>
<sharesOwnedFollowingTransaction><value>90</value><footnoteId id="F1"/>
</sharesOwnedFollowingTransaction></postTransactionAmounts>
<ownershipNature><directOrIndirectOwnership><value>D</value>
</directOrIndirectOwnership></ownershipNature>
</nonDerivativeTransaction></nonDerivativeTable>
<footnotes><footnote id="F1">Includes restricted shares.</footnote></footnotes>
<ownerSignature><signatureName>/s/ Owner One</signatureName>
<signatureDate>2026-02-10</signatureDate></ownerSignature>
</ownershipDocument>`
	path := writeOwnershipSubmission(t, "4", "0000000001-26-000001",
		testDocument{documentType: "4", filename: "form4.xml", content: xml})

	filing, err := Parse(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	if filing.Family != FamilyInsider || len(filing.Parties) != 2 ||
		len(filing.InsiderTransactions) != 1 || len(filing.Footnotes) != 1 ||
		len(filing.Signatures) != 1 {
		t.Fatalf("unexpected filing: %+v", filing)
	}

	transaction := filing.InsiderTransactions[0]
	if transaction.Amount != "10" || transaction.SharesAfter != "90" ||
		transaction.FootnoteRefs != "F1" || transaction.Price != "25.50" {
		t.Fatalf("unexpected transaction: %+v", transaction)
	}
}

func TestParse13F(t *testing.T) {
	t.Parallel()

	primary := `<edgarSubmission><headerData><submissionType>13F-HR/A</submissionType>
<filerInfo><filer><credentials><cik>0000000300</cik></credentials></filer>
<periodOfReport>12-31-2025</periodOfReport></filerInfo></headerData>
<previousAccessionNumber>0000000001-26-000001</previousAccessionNumber>
<formData><coverPage><reportCalendarOrQuarter>12-31-2025</reportCalendarOrQuarter>
<isAmendment>true</isAmendment><amendmentNo>1</amendmentNo>
<amendmentInfo><amendmentType>NEW HOLDINGS</amendmentType></amendmentInfo>
<filingManager><name>Manager LLC</name></filingManager>
<reportType>13F HOLDINGS REPORT</reportType><otherManagersInfo><otherManager>
<form13FFileNumber>028-1</form13FFileNumber><name>Parent Manager</name>
</otherManager></otherManagersInfo></coverPage><summaryPage>
<otherIncludedManagersCount>1</otherIncludedManagersCount>
<tableEntryTotal>1</tableEntryTotal><tableValueTotal>2500</tableValueTotal>
<otherManagers2Info><otherManager2><otherManager><name>Included Manager</name>
<form13FFileNumber>028-2</form13FFileNumber></otherManager></otherManager2>
</otherManagers2Info></summaryPage></formData></edgarSubmission>`
	information := `<informationTable><infoTable><nameOfIssuer>Example Inc</nameOfIssuer>
<titleOfClass>COM</titleOfClass><cusip>123456789</cusip><value>2500</value>
<shrsOrPrnAmt><sshPrnamt>100</sshPrnamt><sshPrnamtType>SH</sshPrnamtType>
</shrsOrPrnAmt><investmentDiscretion>SOLE</investmentDiscretion>
<votingAuthority><Sole>100</Sole><Shared>0</Shared><None>0</None>
</votingAuthority></infoTable></informationTable>`
	path := writeOwnershipSubmission(t, "13F-HR/A",
		"0000000001-26-000002",
		testDocument{
			documentType: "13F-HR/A", filename: "primary.xml",
			content: primary,
		},
		testDocument{
			documentType: "INFORMATION TABLE", filename: "info.xml",
			content: information,
		})

	filing, err := Parse(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	if filing.AmendmentType != "NEW HOLDINGS" ||
		filing.AmendmentNumber != "1" ||
		filing.ReportKind != "13F HOLDINGS REPORT" ||
		filing.PreviousAccession != "0000000001-26-000001" ||
		filing.InstitutionalSummary == nil ||
		filing.InstitutionalSummary.TotalValue != "2500" ||
		len(filing.InstitutionalPositions) != 1 ||
		len(filing.InstitutionalNotices) != 2 {
		t.Fatalf("unexpected filing: %+v", filing)
	}

	position := filing.InstitutionalPositions[0]
	if position.CUSIP != "123456789" || position.Amount != "100" ||
		position.ReportPeriod != "2025-12-31" {
		t.Fatalf("unexpected position: %+v", position)
	}
}

func TestParseStructuredSchedule13(t *testing.T) {
	t.Parallel()

	xml := `<edgarSubmission><headerData><submissionType>SCHEDULE 13G</submissionType>
</headerData><formData><coverPageHeader><securitiesClassTitle>Common Stock
</securitiesClassTitle><dateOfEvent>02/01/2026</dateOfEvent><issuerInfo>
<issuerCIK>0000000400</issuerCIK><issuerName>Issuer Corp</issuerName>
<issuerCusips><issuerCusipNumber>987654321</issuerCusipNumber>
</issuerCusips></issuerInfo></coverPageHeader><reportingPersons>
<reportingPersonInfo><reportingPersonCIK>0000000500</reportingPersonCIK>
<reportingPersonName>Large Owner</reportingPersonName>
<soleVotingPower>10</soleVotingPower><sharedVotingPower>20</sharedVotingPower>
<soleDispositivePower>30</soleDispositivePower>
<sharedDispositivePower>40</sharedDispositivePower>
<aggregateAmountOwned>100</aggregateAmountOwned>
<percentOfClass>6.5</percentOfClass><typeOfReportingPerson>IA
</typeOfReportingPerson><fundType>AF</fundType><memberOfGroup>a</memberOfGroup>
</reportingPersonInfo></reportingPersons><item1><securityAndIssuer>
Example narrative.</securityAndIssuer></item1>
</formData></edgarSubmission>`
	path := writeOwnershipSubmission(t, "SCHEDULE 13G",
		"0000000001-26-000003",
		testDocument{
			documentType: "SCHEDULE 13G", filename: "primary_doc.xml",
			content: xml,
		})

	filing, err := Parse(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	if len(filing.BeneficialReports) != 1 || len(filing.Securities) != 1 ||
		len(filing.BeneficialNarratives) != 1 {
		t.Fatalf("unexpected filing: %+v", filing)
	}

	report := filing.BeneficialReports[0]
	if report.Shares != "100" || report.Percent != "6.5" ||
		report.CUSIP != "987654321" || report.EventDate != "2026-02-01" ||
		report.FilerCategory != "AF" ||
		filing.Parties[1].MemberOfGroup != "a" {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestParseRecognizedLegacyFormReturnsUnsupported(t *testing.T) {
	t.Parallel()

	path := writeOwnershipSubmission(t, "SC 13D", "0000000001-26-000004",
		testDocument{
			documentType: "SC 13D", filename: "legacy.htm",
			content: `<html/>`,
		})

	filing, err := Parse(t.Context(), path)
	if err == nil || !strings.Contains(err.Error(), ErrUnsupported.Error()) {
		t.Fatalf("error = %v", err)
	}

	if filing.Accession != "0000000001-26-000004" {
		t.Fatalf("filing = %+v", filing)
	}
}
