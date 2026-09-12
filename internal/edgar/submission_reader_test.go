package edgar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testInstanceFilename = "test.xml"

const testInstance = `<?xml version="1.0"?>
<i:xbrl xmlns:i="http://www.xbrl.org/2003/instance" xmlns:t="urn:test"
 xmlns:d="http://xbrl.org/2006/xbrldi" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
 xmlns:l="http://www.xbrl.org/2003/linkbase" xmlns:x="http://www.w3.org/1999/xlink" xml:lang="en">
 <l:schemaRef x:href="test.xsd" x:type="simple"/>
 <i:context id="c"><i:entity><i:identifier scheme="urn:cik">123</i:identifier>
 <i:segment><d:explicitMember dimension="t:Axis">t:Member</d:explicitMember></i:segment></i:entity>
 <i:period><i:startDate>2024-01-01</i:startDate><i:endDate>2024-12-31</i:endDate></i:period>
 <i:scenario><d:typedMember dimension="t:TypedAxis"><t:Code>A&amp;B</t:Code></d:typedMember></i:scenario></i:context>
 <i:context id="instant"><i:entity><i:identifier scheme="urn:cik">123</i:identifier></i:entity>
 <i:period><i:instant>2024-12-31</i:instant></i:period></i:context>
 <i:unit id="u"><i:measure xmlns:money="urn:currency">money:USD</i:measure></i:unit>
 <i:unit id="ratio"><i:divide><i:unitNumerator><i:measure>t:USD</i:measure></i:unitNumerator>
 <i:unitDenominator><i:measure>i:shares</i:measure></i:unitDenominator></i:divide></i:unit>
 <t:Revenue id="f1" contextRef="c" unitRef="u" decimals="2">9007199254740993.01</t:Revenue>
 <t:Revenue id="f2" contextRef="c" unitRef="u" decimals="2">9007199254740993.01</t:Revenue>
 <t:Zero contextRef="instant" unitRef="ratio" precision="INF">0</t:Zero>
 <t:Absent contextRef="c" unitRef="u" xsi:nil="true"/>
 <t:Text contextRef="c"> A &amp; B </t:Text>
 <l:footnoteLink x:type="extended"><l:loc x:type="locator" x:label="fact" x:href="#f1"/>
 <l:footnote x:type="resource" x:label="note">First <t:b>note</t:b>.</l:footnote>
 <l:footnoteArc x:type="arc" x:from="fact" x:to="note"/></l:footnoteLink>
</i:xbrl>`

func submissionText(payload, filename string) string {
	return "<SEC-DOCUMENT>0000000123-24-000001.txt\n<SEC-HEADER>header\n" +
		"ACCESSION NUMBER: 0000000123-24-000001\nCONFORMED SUBMISSION TYPE: 10-K\n" +
		"CENTRAL INDEX KEY: 0000000123\nFILED AS OF DATE: 20250131\nCONFORMED PERIOD OF REPORT: 20241231\n" +
		"ITEM INFORMATION: first\nITEM INFORMATION: second\n\tUNKNOWN FIELD: retained\n" +
		"</SEC-HEADER>\n<DOCUMENT>\n<TYPE>XML\n<SEQUENCE>1\n<FILENAME>" + filename +
		"\n<DESCRIPTION>test instance\n<TEXT>\n<XML>\n" + payload + "\n</XML>\n</TEXT>\n</DOCUMENT>\n</SEC-DOCUMENT>\n"
}

func writeSubmission(t *testing.T, source string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "submission.txt")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestParseGoldenSubmissions(t *testing.T) {
	const annualForm = "10-K"
	for _, test := range []struct {
		form      string
		documents int
		accession string
		facts     int
		contexts  int
		units     int
	}{
		{form: annualForm, documents: 130, accession: "0001193125-26-323660", facts: 1869, contexts: 446, units: 6},
		{form: "10-Q", documents: 106, accession: "0000950170-25-010491", facts: 1580, contexts: 409, units: 5},
		{form: "8-K", documents: 10, accession: "0001193125-26-191457", facts: 28, contexts: 4, units: 0},
	} {
		t.Run(test.form, func(t *testing.T) {
			path := filepath.Join("..", "..", "golden", "sample_"+test.form+".txt")

			filing, err := ParseSubmission(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}

			if filing.Status != ParseComplete || len(filing.Documents) != test.documents || len(filing.Instances) != 1 {
				t.Fatalf("status=%s documents=%d instances=%d diagnostics=%+v", filing.Status, len(filing.Documents), len(filing.Instances), filing.Diagnostics)
			}

			if filing.AccessionNumber() != test.accession || filing.FormType() != test.form || filing.Metadata.CIK != "0000789019" {
				t.Fatalf("unexpected metadata: %+v", filing.Metadata)
			}

			instance := &filing.Instances[0]
			if len(instance.Facts) != test.facts || len(instance.Contexts) != test.contexts || len(instance.Units) != test.units {
				t.Fatal("XBRL fixture counts changed")
			}

			checkGoldenReferences(t, instance, test.form)

			diagnosticCount := 0
			if test.form == "10-Q" {
				diagnosticCount = 1
			}

			if len(filing.Diagnostics) != diagnosticCount {
				t.Fatalf("unexpected diagnostics: %+v", filing.Diagnostics)
			}

			t.Logf("facts=%d contexts=%d units=%d", len(instance.Facts), len(instance.Contexts), len(instance.Units))
		})
	}
}

func checkGoldenReferences(t *testing.T, instance *XBRLInstance, form string) {
	t.Helper()

	if len(instance.References) == 0 {
		t.Fatal("schema references missing")
	}

	foundForm := false

	for _, fact := range instance.Facts {
		if _, ok := instance.Context(fact.ContextRef); !ok {
			t.Fatalf("missing context %q", fact.ContextRef)
		}

		if fact.UnitRef != "" {
			if _, ok := instance.Unit(fact.UnitRef); !ok {
				t.Fatalf("missing unit %q", fact.UnitRef)
			}
		}

		if fact.Concept.Local == "DocumentType" && strings.HasPrefix(fact.Concept.Namespace, "http://xbrl.sec.gov/dei/") {
			foundForm = fact.Value == form
		}
	}

	if !foundForm {
		t.Fatal("XBRL document type does not match submission")
	}
}

func TestSubmissionOffsetsAndHeader(t *testing.T) {
	source := strings.ReplaceAll(submissionText(testInstance, "anything.xml"), "\n", "\r\n")
	path := writeSubmission(t, source)

	filing, err := ParseSubmission(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte(source))
	if filing.SourceSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("checksum mismatch")
	}

	if strings.Count(strings.Join(filing.Metadata.Header, "\n"), "ITEM INFORMATION:") != 2 {
		t.Fatal("repeated header fields lost")
	}

	if !strings.Contains(strings.Join(filing.Metadata.Header, "\n"), "\tUNKNOWN FIELD: retained") {
		t.Fatal("unknown header field lost")
	}

	if filing.FilingDate() != "20250131" || filing.ReportDate() != "20241231" {
		t.Fatal("unexpected dates")
	}

	document := filing.Documents[0]

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	payload, err := io.ReadAll(io.NewSectionReader(file, document.ContentOffset, document.ContentLength))
	if err != nil {
		t.Fatal(err)
	}

	if string(payload) != strings.ReplaceAll(testInstance, "\n", "\r\n")+"\r\n" {
		t.Fatal("incorrect content byte range")
	}

	if document.Role != "xbrl_instance" || document.Sequence != "1" || document.Description != "test instance" {
		t.Fatalf("unexpected document: %+v", document)
	}
}

func TestSubmissionLongLinesAndNoXBRL(t *testing.T) {
	source := submissionText("<html>"+strings.Repeat("x", 200000)+"</html>", "report.htm")

	filing, err := ParseSubmission(t.Context(), writeSubmission(t, source))
	if err != nil {
		t.Fatal(err)
	}

	if filing.Status != ParseNoXBRL || len(filing.Instances) != 0 {
		t.Fatalf("unexpected status %s", filing.Status)
	}
}

func TestSubmissionRejectsMalformedEnvelope(t *testing.T) {
	source := submissionText(testInstance, testInstanceFilename)
	for name, input := range map[string]string{
		"wrong root":           "<html/>\n",
		"truncated":            strings.TrimSuffix(source, "</SEC-DOCUMENT>\n"),
		"missing text end":     strings.ReplaceAll(source, "</TEXT>\n", ""),
		"missing document end": strings.ReplaceAll(source, "</DOCUMENT>\n", ""),
		"missing wrapper end":  strings.ReplaceAll(source, "</XML>\n", ""),
		"missing header end":   strings.ReplaceAll(source, "</SEC-HEADER>\n", ""),
		"trailing content":     source + "unexpected",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSubmission(t.Context(), writeSubmission(t, input)); err == nil {
				t.Fatal("expected envelope error")
			}
		})
	}

	if _, err := ParseSubmission(t.Context(), "submission.xml"); err == nil {
		t.Fatal("expected extension error")
	}
}

func TestSubmissionExtractionStatuses(t *testing.T) {
	const partialStatus = ParsePartial
	for _, test := range []struct{ name, payload, filename, status, code string }{
		{
			name: "inline only", payload: `<html xmlns:ix="http://www.xbrl.org/2013/inlineXBRL"/>`,
			filename: "report.htm", status: ParseUnsupported, code: "inline_only",
		},
		{
			name: "broken XML", payload: strings.TrimSuffix(testInstance, "</i:xbrl>"),
			filename: testInstanceFilename, status: partialStatus, code: "xbrl_error",
		},
		{
			name: "missing context", payload: strings.ReplaceAll(testInstance, `contextRef="c"`, `contextRef="missing"`),
			filename: testInstanceFilename, status: partialStatus, code: "xbrl_reference",
		},
		{
			name: "missing unit", payload: strings.ReplaceAll(testInstance, `unitRef="u"`, `unitRef="missing"`),
			filename: testInstanceFilename, status: partialStatus, code: "xbrl_reference",
		},
		{
			name: "tuple", payload: strings.ReplaceAll(testInstance, "</i:xbrl>",
				`<t:Tuple><t:Value contextRef="c">1</t:Value></t:Tuple></i:xbrl>`),
			filename: testInstanceFilename, status: partialStatus, code: "unsupported_xbrl",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			filing, err := ParseSubmission(t.Context(), writeSubmission(t, submissionText(test.payload, test.filename)))
			if err != nil {
				t.Fatal(err)
			}

			if filing.Status != test.status || len(filing.Diagnostics) == 0 || filing.Diagnostics[0].Code != test.code {
				t.Fatalf("status=%s diagnostics=%+v", filing.Status, filing.Diagnostics)
			}
		})
	}
}

func TestSubmissionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := ParseSubmission(ctx, writeSubmission(t, submissionText(testInstance, testInstanceFilename)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
}

func TestSubmissionFilers(t *testing.T) {
	header := "SUBJECT COMPANY:\n\tCENTRAL INDEX KEY: 999\n" +
		"FILER:\n\tCOMPANY DATA:\n\t\tCENTRAL INDEX KEY: 123\n\t\tCOMPANY CONFORMED NAME: First\n" +
		"FILER:\n\tCOMPANY DATA:\n\t\tCENTRAL INDEX KEY: 456\n\t\tCOMPANY CONFORMED NAME: Second\n"
	source := strings.ReplaceAll(submissionText(testInstance, testInstanceFilename), "</SEC-HEADER>", header+"</SEC-HEADER>")

	filing, err := ParseSubmission(t.Context(), writeSubmission(t, source))
	if err != nil {
		t.Fatal(err)
	}

	if filing.Metadata.CIK != "123" || len(filing.Metadata.Filers) != 2 {
		t.Fatalf("incorrect filer identities: %+v", filing.Metadata)
	}

	if filing.Metadata.Filers[1].CIK != "456" || filing.Metadata.Filers[1].Name != "Second" {
		t.Fatal("second filer metadata lost")
	}

	if len(filing.Metadata.Items) != 2 {
		t.Fatal("repeated items lost")
	}
}

func TestSubmissionWrappersAndAttachments(t *testing.T) {
	source := submissionText(testInstance, testInstanceFilename)
	for _, text := range []string{
		strings.ReplaceAll(strings.ReplaceAll(source, "<XML>\n", ""), "</XML>\n", ""),
		strings.ReplaceAll(source, "XML>", "XBRL>"),
	} {
		filing, err := ParseSubmission(t.Context(), writeSubmission(t, text))
		if err != nil {
			t.Fatal(err)
		}

		if filing.Status != ParseComplete {
			t.Fatalf("wrapper status=%s", filing.Status)
		}
	}

	filing, err := ParseSubmission(t.Context(), writeSubmission(t, submissionText("body { color: black; }", "report.css")))
	if err != nil {
		t.Fatal(err)
	}

	if len(filing.Diagnostics) != 0 || filing.Documents[0].Type != "XML" {
		t.Fatal("non-XML attachment was parsed as XML")
	}
}

func TestSubmissionMultipleInstancesKeepReferenceScopes(t *testing.T) {
	first := submissionText(testInstance, "first.xml")
	second := submissionText(strings.ReplaceAll(testInstance, "9007199254740993.01", "42"), "second.xml")
	start := strings.Index(second, documentStartTag)
	text := strings.ReplaceAll(first, submissionEndTag+"\n", second[start:])

	filing, err := ParseSubmission(t.Context(), writeSubmission(t, text))
	if err != nil {
		t.Fatal(err)
	}

	if filing.Status != ParseComplete || len(filing.Instances) != 2 {
		t.Fatalf("unexpected multi-instance result: %s", filing.Status)
	}

	if filing.Instances[0].Facts[0].Value == filing.Instances[1].Facts[0].Value {
		t.Fatal("fact occurrences merged")
	}

	if filing.Instances[1].DocumentIndex != 1 || filing.Instances[1].Document != "second.xml" {
		t.Fatal("document provenance lost")
	}
}
