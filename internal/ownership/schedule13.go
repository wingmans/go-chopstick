package ownership

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"wingman.com/chopstick/internal/edgar"
)

func parseSchedule13(ctx context.Context, path string,
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
		return fmt.Errorf("unexpected Schedule 13 root %q", root.name)
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

	cover := formData.child("coverPageHeader")
	if cover == nil {
		return errors.New("schedule 13 has no cover page header")
	}

	issuer := cover.child("issuerInfo")
	if issuer == nil {
		return errors.New("schedule 13 has no issuer")
	}

	issuerCIK := issuer.value("issuerCIK")
	issuerName := issuer.value("issuerName")
	filing.Parties = append(filing.Parties, Party{
		Key: "issuer", CIK: issuerCIK, Name: issuerName, Role: "issuer",
	})

	cusips := scheduleCUSIPs(issuer)
	securityTitle := cover.value("securitiesClassTitle")

	primaryCUSIP := ""
	if len(cusips) > 0 {
		primaryCUSIP = cusips[0]
	}

	filing.Securities = append(filing.Securities, Security{
		Key: "security", IssuerKey: "issuer", Title: securityTitle,
		Class: securityTitle, Kind: "non_derivative", CUSIP: primaryCUSIP,
		AlternateCUSIPs: append([]string(nil), cusips[1:]...),
	})

	eventDate := normalizeDate(cover.value("dateOfEvent"))
	if eventDate != "" {
		filing.ReportDate = eventDate
	}

	filing.AmendmentNumber = cover.value("amendmentNo")
	if filing.Amendment {
		filing.AmendmentType = "STATE UPDATE"
	}

	reportingPersons := formData.path("reportingPersons")
	if reportingPersons == nil {
		return errors.New("schedule 13 has no reporting persons")
	}

	for index, person := range reportingPersons.childrenNamed(
		"reportingPersonInfo") {
		key := fmt.Sprintf("owner:%d", index+1)
		types := childTexts(person, "typeOfReportingPerson")
		filing.Parties = append(filing.Parties, Party{
			Key: key, CIK: person.value("reportingPersonCIK"),
			Name: person.value("reportingPersonName"),
			Role: "reporting_owner", Type: strings.Join(types, ","),
			MemberOfGroup: person.value("memberOfGroup"),
		})
		filing.BeneficialReports = append(filing.BeneficialReports,
			BeneficialReport{
				Row: index + 1, IssuerKey: "issuer", OwnerKey: key,
				SecurityKey: "security", CUSIP: primaryCUSIP,
				EventDate:         eventDate,
				Shares:            person.value("aggregateAmountOwned"),
				Percent:           person.value("percentOfClass"),
				SoleVotingPower:   person.value("soleVotingPower"),
				SharedVotingPower: person.value("sharedVotingPower"),
				SoleDispositivePower: person.value(
					"soleDispositivePower"),
				SharedDispositivePower: person.value(
					"sharedDispositivePower"),
				ReportingPersonType: strings.Join(types, ","),
				FilerCategory: strings.Join(
					childTexts(person, "fundType"), ","),
			})
	}

	if len(filing.BeneficialReports) == 0 {
		return errors.New("schedule 13 has no reporting person states")
	}

	filing.BeneficialNarratives = scheduleNarratives(formData)

	return nil
}

func scheduleNarratives(formData *xmlElement) []BeneficialNarrative {
	var result []BeneficialNarrative

	for _, item := range formData.children {
		if !strings.HasPrefix(item.name, "item") {
			continue
		}

		collectScheduleNarratives(item, item.name, "", &result)
	}

	return result
}

func collectScheduleNarratives(element *xmlElement, item, path string,
	result *[]BeneficialNarrative,
) {
	if len(element.children) == 0 {
		text := element.text()
		if text == "" {
			return
		}

		*result = append(*result, BeneficialNarrative{
			Row: len(*result) + 1, Item: item, Field: path, Text: text,
		})

		return
	}

	for _, child := range element.children {
		childPath := child.name
		if path != "" {
			childPath = path + "." + child.name
		}

		collectScheduleNarratives(child, item, childPath, result)
	}
}

func scheduleCUSIPs(issuer *xmlElement) []string {
	var values []string

	for _, name := range []string{
		"issuerCusipNumber", "issuerCUSIP", "issuerCusip",
	} {
		for _, element := range issuer.descendants(name) {
			value := strings.ToUpper(strings.TrimSpace(element.text()))
			if value != "" && !containsString(values, value) {
				values = append(values, value)
			}
		}
	}

	return values
}

func childTexts(element *xmlElement, name string) []string {
	var values []string

	for _, child := range element.childrenNamed(name) {
		if value := child.text(); value != "" {
			values = append(values, value)
		}
	}

	return values
}

func containsString(values []string, candidate string) bool {
	return slices.Contains(values, candidate)
}
