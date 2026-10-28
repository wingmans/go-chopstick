package ownership

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"wingman.com/fetch-ecb/internal/edgar"
)

func parse13F(ctx context.Context, path string,
	documents []edgar.SubmissionDocument, filing *Filing,
) error {
	document, err := primaryDocument(documents, filing.FormType)
	if err != nil {
		return err
	}

	root, err := parseDocument(ctx, path, document)
	if err != nil {
		return err
	}

	if root.name != "edgarSubmission" {
		return fmt.Errorf("unexpected Form 13F root %q", root.name)
	}

	submissionType := NormalizeFormType(firstDescendantText(root,
		"submissionType"))
	if submissionType != filing.FormType {
		return fmt.Errorf("document type %q does not match submission %q",
			submissionType, filing.FormType)
	}

	formData := root.child("formData")
	filing.PreviousAccession = firstDescendantText(root,
		"previousAccessionNumber")

	cover := formData.child("coverPage")
	if cover == nil {
		return errors.New("form 13F has no cover page")
	}

	reportPeriod := normalizeDate(cover.value("reportCalendarOrQuarter"))
	if reportPeriod == "" {
		reportPeriod = normalizeDate(firstDescendantText(root,
			"periodOfReport"))
	}

	if reportPeriod != "" {
		filing.ReportDate = reportPeriod
	}

	managerCIK := ""
	if credentials := root.path("headerData", "filerInfo", "filer",
		"credentials"); credentials != nil {
		managerCIK = credentials.value("cik")
	}

	if managerCIK == "" {
		managerCIK = firstDescendantText(root, "cik")
	}

	managerName := cover.value("filingManager", "name")
	if managerName == "" {
		return errors.New("form 13F has no filing manager name")
	}

	filing.Parties = append(filing.Parties, Party{
		Key: "manager", CIK: managerCIK, Name: managerName,
		Role: "manager",
	})
	filing.Amendment = filing.Amendment ||
		parseBoolean(cover.value("isAmendment"))
	filing.AmendmentType = normalizeAmendmentType(
		cover.value("amendmentInfo", "amendmentType"))

	filing.AmendmentNumber = cover.value("amendmentNo")

	filing.ReportKind = strings.ToUpper(strings.Join(strings.Fields(
		cover.value("reportType")), " "))
	if filing.Amendment && filing.AmendmentType == "" {
		filing.AmendmentType = "RESTATEMENT"
	}

	row := 0
	for _, manager := range cover.path("otherManagersInfo").childrenNamed(
		"otherManager") {
		row++
		filing.InstitutionalNotices = append(filing.InstitutionalNotices,
			InstitutionalNotice{
				Row: row, ManagerKey: "manager", ReportPeriod: reportPeriod,
				Kind:                 "reporting",
				ReportingManagerName: manager.value("name"),
				ReportingManagerFile: manager.value("form13FFileNumber"),
			})
	}

	summary := formData.child("summaryPage")
	if summary != nil {
		filing.InstitutionalSummary = &InstitutionalSummary{
			EntryCount:           summary.value("tableEntryTotal"),
			TotalValue:           summary.value("tableValueTotal"),
			IncludedManagerCount: summary.value("otherIncludedManagersCount"),
		}
	}

	for _, wrapper := range summary.path("otherManagers2Info").childrenNamed(
		"otherManager2") {
		manager := wrapper.child("otherManager")
		if manager == nil {
			continue
		}

		row++
		filing.InstitutionalNotices = append(filing.InstitutionalNotices,
			InstitutionalNotice{
				Row: row, ManagerKey: "manager", ReportPeriod: reportPeriod,
				Kind:                 "included",
				ReportingManagerName: manager.value("name"),
				ReportingManagerFile: manager.value("form13FFileNumber"),
			})
	}

	for _, informationDocument := range documentsOfType(documents,
		"INFORMATION TABLE") {
		positions, err := parse13FInformationTable(ctx, path,
			informationDocument, len(filing.InstitutionalPositions),
			reportPeriod)
		if err != nil {
			return err
		}

		filing.InstitutionalPositions = append(
			filing.InstitutionalPositions, positions...)
	}

	for _, position := range filing.InstitutionalPositions {
		key := "cusip:" + position.CUSIP
		found := false

		for _, security := range filing.Securities {
			if security.Key == key {
				found = true

				break
			}
		}

		if !found {
			filing.Securities = append(filing.Securities, Security{
				Key: key, Title: position.IssuerName,
				Class: position.Class, Kind: "reported", CUSIP: position.CUSIP,
			})
		}
	}

	return nil
}

func parse13FInformationTable(ctx context.Context, path string,
	document edgar.SubmissionDocument, rowOffset int, reportPeriod string,
) ([]InstitutionalPosition, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open submission: %w", err)
	}
	defer func() { _ = file.Close() }()

	decoder := xml.NewDecoder(io.NewSectionReader(file, document.ContentOffset,
		document.ContentLength))

	var positions []InstitutionalPosition

	seenRoot := false

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", document.Filename, err)
		}

		switch value := token.(type) {
		case xml.StartElement:
			if !seenRoot {
				if value.Name.Local != "informationTable" {
					return nil, fmt.Errorf("unexpected information table root %q",
						value.Name.Local)
				}

				seenRoot = true

				continue
			}

			if value.Name.Local != "infoTable" {
				continue
			}

			element, err := readXMLElement(ctx, decoder, value, 1)
			if err != nil {
				return nil, fmt.Errorf("decode %s row: %w",
					document.Filename, err)
			}

			position, err := read13FPosition(element,
				rowOffset+len(positions)+1, reportPeriod)
			if err != nil {
				return nil, fmt.Errorf("decode %s row %d: %w",
					document.Filename, rowOffset+len(positions)+1, err)
			}

			positions = append(positions, position)
		case xml.Directive:
			return nil, errors.New("XML directives are not supported")
		}
	}

	if !seenRoot {
		return nil, errors.New("information table has no XML root")
	}

	return positions, nil
}

func read13FPosition(element *xmlElement, row int,
	reportPeriod string,
) (InstitutionalPosition, error) {
	position := InstitutionalPosition{
		Row: row, ManagerKey: "manager", ReportPeriod: reportPeriod,
		IssuerName:           element.value("nameOfIssuer"),
		Class:                element.value("titleOfClass"),
		CUSIP:                strings.ToUpper(element.value("cusip")),
		FIGI:                 strings.ToUpper(element.value("figi")),
		Value:                element.value("value"),
		Amount:               element.value("shrsOrPrnAmt", "sshPrnamt"),
		AmountType:           element.value("shrsOrPrnAmt", "sshPrnamtType"),
		PutCall:              element.value("putCall"),
		InvestmentDiscretion: element.value("investmentDiscretion"),
		OtherManagers:        element.value("otherManager"),
		VotingSole:           element.value("votingAuthority", "Sole"),
		VotingShared:         element.value("votingAuthority", "Shared"),
		VotingNone:           element.value("votingAuthority", "None"),
	}
	if position.IssuerName == "" || position.CUSIP == "" {
		return InstitutionalPosition{},
			errors.New("position is missing issuer name or CUSIP")
	}

	return position, nil
}

func firstDescendantText(root *xmlElement, name string) string {
	values := root.descendants(name)
	if len(values) == 0 {
		return ""
	}

	return values[0].text()
}

func normalizeAmendmentType(value string) string {
	value = strings.ToUpper(strings.Join(strings.Fields(value), " "))
	switch value {
	case "NEW HOLDINGS", "NEW HOLDING", "ADD NEW HOLDINGS":
		return "NEW HOLDINGS"
	case "RESTATEMENT", "RESTATED":
		return "RESTATEMENT"
	default:
		return value
	}
}
