package filingworkflow

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

const testInstanceFilename = "test_htm.xml"

const testInstance = `<?xml version="1.0"?>
<xbrl xmlns="http://www.xbrl.org/2003/instance" xmlns:t="urn:test">
 <context id="c"><entity><identifier scheme="urn:cik">123</identifier></entity>
 <period><startDate>2024-01-01</startDate><endDate>2024-12-31</endDate></period></context>
 <unit id="u"><measure>t:USD</measure></unit>
 <t:Revenue contextRef="c" unitRef="u">42</t:Revenue>
</xbrl>`

func submissionText(payload, filename string) string {
	return "<SEC-DOCUMENT>0000000123-24-000001.txt\n<SEC-HEADER>\n" +
		"ACCESSION NUMBER: 0000000123-24-000001\nCONFORMED SUBMISSION TYPE: 10-K\n" +
		"CENTRAL INDEX KEY: 0000000123\nFILED AS OF DATE: 20250131\n" +
		"CONFORMED PERIOD OF REPORT: 20241231\n</SEC-HEADER>\n<DOCUMENT>\n" +
		"<TYPE>XML\n<SEQUENCE>1\n<FILENAME>" + filename + "\n<DESCRIPTION>test\n<TEXT>\n" +
		"<XML>\n" + payload + "\n</XML>\n</TEXT>\n</DOCUMENT>\n</SEC-DOCUMENT>\n"
}

func writeSubmission(t *testing.T, source string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "submission.txt")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

type roundTripper func(*http.Request) (*http.Response, error)

func (r roundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return r(request)
}
