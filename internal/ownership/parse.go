package ownership

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"wingman.com/fetch-ecb/internal/edgar"
)

var ErrUnsupported = errors.New("ownership form parser is not implemented")

// Parse reads one cached SEC submission and dispatches its structured
// documents to the appropriate ownership parser.
func Parse(ctx context.Context, path string) (Filing, error) {
	parsed, err := edgar.ParseSubmission(ctx, path)
	if err != nil {
		return Filing{}, err
	}

	form := NormalizeFormType(parsed.Metadata.FormType)

	family, recognized := Family(form)
	if !recognized {
		return Filing{}, fmt.Errorf("form %q is not an ownership form", form)
	}

	filing := Filing{
		Accession:     parsed.Metadata.Accession,
		FormType:      form,
		Family:        family,
		FilingDate:    normalizeDate(parsed.Metadata.FilingDate),
		ReportDate:    normalizeDate(parsed.Metadata.ReportDate),
		SourcePath:    path,
		SourceSHA256:  parsed.SourceSHA256,
		ParserVersion: ParserVersion,
		Amendment:     IsAmendment(form),
	}
	if !SupportsForm(form) {
		return filing, fmt.Errorf("%w: %s", ErrUnsupported, form)
	}

	switch family {
	case FamilyInsider:
		err = parseSection16(ctx, path, parsed.Documents, &filing)
	case FamilyInstitutional:
		err = parse13F(ctx, path, parsed.Documents, &filing)
	case FamilyBeneficial:
		err = parseSchedule13(ctx, path, parsed.Documents, &filing)
	default:
		err = fmt.Errorf("unknown ownership family %q", family)
	}

	if err != nil {
		return filing, fmt.Errorf("parse %s filing %s: %w",
			form, filing.Accession, err)
	}

	return filing, nil
}

func parseDocument(ctx context.Context, path string,
	document edgar.SubmissionDocument,
) (*xmlElement, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open submission: %w", err)
	}
	defer func() { _ = file.Close() }()

	if document.ContentOffset < 0 || document.ContentLength < 0 {
		return nil, fmt.Errorf("document %s has invalid byte range",
			document.Filename)
	}

	root, err := decodeXML(ctx, io.NewSectionReader(file,
		document.ContentOffset, document.ContentLength))
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", document.Filename, err)
	}

	return root, nil
}

func primaryDocument(documents []edgar.SubmissionDocument,
	form string,
) (edgar.SubmissionDocument, error) {
	for _, document := range documents {
		if NormalizeFormType(document.Type) == form {
			return document, nil
		}
	}

	return edgar.SubmissionDocument{}, fmt.Errorf(
		"submission has no %s primary XML document", form)
}

func documentsOfType(documents []edgar.SubmissionDocument,
	documentType string,
) []edgar.SubmissionDocument {
	var result []edgar.SubmissionDocument

	for _, document := range documents {
		if strings.EqualFold(strings.TrimSpace(document.Type), documentType) {
			result = append(result, document)
		}
	}

	return result
}

func normalizeDate(value string) string {
	value = strings.TrimSpace(value)
	for _, layout := range []string{
		"2006-01-02", "20060102", "01-02-2006", "01/02/2006",
	} {
		date, err := time.Parse(layout, value)
		if err == nil {
			return date.Format("2006-01-02")
		}
	}

	return value
}
