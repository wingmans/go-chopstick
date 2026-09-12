package edgar

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	envelopeDone     = "done"
	envelopeBetween  = "between"
	submissionEndTag = "</SEC-DOCUMENT>"
	documentEndTag   = "</DOCUMENT>"
	documentStartTag = "<DOCUMENT>"
)

// ParseSubmission reads a local .txt SEC envelope and its extracted XBRL instances.
// Envelope errors return an error; extraction limitations are recorded in Status
// and Diagnostics so a readable submission inventory remains available.
func ParseSubmission(ctx context.Context, path string) (*ParsedFiling, error) {
	if filepath.Ext(path) != ".txt" {
		return nil, errors.New("submission must have a .txt extension")
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open submission: %w", err)
	}

	defer func() { _ = file.Close() }()

	before, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat submission: %w", err)
	}

	hash := sha256.New()

	var result ParsedFiling

	result.SchemaVersion = ParsedSchemaVersion
	result.ParserVersion = ParserVersion
	result.SourcePath = filepath.Clean(path)

	result.Status = ParseNoXBRL
	if err := readEnvelope(ctx, io.TeeReader(file, hash), &result); err != nil {
		return nil, fmt.Errorf("read submission %s: %w", path, err)
	}

	result.SourceSHA256 = hex.EncodeToString(hash.Sum(nil))
	if err := result.extractInstances(ctx, file); err != nil {
		return nil, err
	}

	after, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat parsed submission: %w", err)
	}

	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("submission changed while being parsed")
	}

	return &result, nil
}

type envelopeReader struct {
	state          string
	position       int64
	document       SubmissionDocument
	wrapper        string
	contentStarted bool
	contentEnded   bool
}

func readEnvelope(ctx context.Context, source io.Reader, result *ParsedFiling) error {
	reader := bufio.NewReader(source)

	var envelope envelopeReader

	envelope.state = "start"

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		if len(line) > 0 {
			start := envelope.position

			envelope.position += int64(len(line))
			if consumeErr := envelope.consume(strings.TrimSpace(line), strings.TrimRight(line, "\r\n"), start, result); consumeErr != nil {
				return fmt.Errorf("byte %d: %w", start, consumeErr)
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
	}

	if envelope.state != envelopeDone {
		return fmt.Errorf("truncated submission in %s", envelope.state)
	}

	result.Metadata.Filers = readFilers(result.Metadata.Header)
	if len(result.Metadata.Filers) > 0 {
		result.Metadata.CIK = result.Metadata.Filers[0].CIK
	}

	if result.Metadata.Accession == "" || result.Metadata.CIK == "" || result.Metadata.FormType == "" {
		return errors.New("submission header is missing accession, CIK, or form type")
	}

	for _, line := range result.Metadata.Header {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "PUBLIC DOCUMENT COUNT:"); ok {
			count, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || count != len(result.Documents) {
				result.diagnostic("document_count", "declared document count differs from inventory", "")
			}
		}
	}

	return nil
}

func (e *envelopeReader) consume(line, raw string, start int64, result *ParsedFiling) error {
	switch e.state {
	case "start":
		if line == "" {
			return nil
		}

		if !strings.HasPrefix(line, "<SEC-DOCUMENT>") {
			return errors.New("expected <SEC-DOCUMENT>")
		}

		e.state = "header_start"
	case "header_start":
		if line == "" {
			return nil
		}

		if !strings.HasPrefix(line, "<SEC-HEADER>") {
			return errors.New("expected <SEC-HEADER>")
		}

		e.state = "header"
	case "header":
		if line == "</SEC-HEADER>" {
			e.state = envelopeBetween

			return nil
		}

		if line == documentStartTag || line == submissionEndTag {
			return errors.New("unclosed SEC header")
		}

		result.Metadata.Header = append(result.Metadata.Header, raw)
		readHeaderField(&result.Metadata, line)
	case envelopeBetween:
		switch line {
		case "":
		case documentStartTag:
			var document SubmissionDocument

			e.document = document
			e.document.Role = "attachment"
			e.wrapper, e.contentStarted, e.contentEnded = "", false, false
			e.state = "document"
		case submissionEndTag:
			e.state = envelopeDone
		default:
			return errors.New("expected <DOCUMENT> or </SEC-DOCUMENT>")
		}
	case "document":
		return e.documentHeader(line)
	case "text":
		return e.documentText(line, start)
	case "document_end":
		if line == "" {
			return nil
		}

		if line != documentEndTag {
			return errors.New("expected </DOCUMENT>")
		}

		result.Documents = append(result.Documents, e.document)
		e.state = envelopeBetween
	case envelopeDone:
		if line != "" {
			return errors.New("unexpected content after </SEC-DOCUMENT>")
		}
	}

	return nil
}

func (e *envelopeReader) documentHeader(line string) error {
	switch {
	case line == "<TEXT>":
		e.document.Offset = e.position
		e.document.ContentOffset = e.position
		e.state = "text"
	case strings.HasPrefix(line, "<TYPE>"):
		e.document.Type = strings.TrimPrefix(line, "<TYPE>")
	case strings.HasPrefix(line, "<SEQUENCE>"):
		e.document.Sequence = strings.TrimPrefix(line, "<SEQUENCE>")
	case strings.HasPrefix(line, "<FILENAME>"):
		e.document.Filename = strings.TrimPrefix(line, "<FILENAME>")
	case strings.HasPrefix(line, "<DESCRIPTION>"):
		e.document.Description = strings.TrimPrefix(line, "<DESCRIPTION>")
	case line == documentStartTag || line == documentEndTag || line == submissionEndTag:
		return errors.New("document is missing <TEXT>")
	}

	return nil
}

func (e *envelopeReader) documentText(line string, start int64) error {
	if line == "</TEXT>" {
		if e.wrapper != "" && !e.contentEnded {
			return errors.New("unclosed payload wrapper")
		}

		e.document.Length = start - e.document.Offset
		if !e.contentEnded {
			e.document.ContentLength = start - e.document.ContentOffset
		}

		e.state = "document_end"

		return nil
	}

	if line == documentEndTag || line == submissionEndTag || line == documentStartTag {
		return errors.New("unclosed document text")
	}

	if e.contentEnded && line != "" {
		return errors.New("content after payload wrapper")
	}

	if !e.contentStarted && line != "" {
		e.contentStarted = true
		if line == "<XML>" || line == "<XBRL>" {
			e.wrapper = strings.Trim(line, "<>")
			e.document.ContentOffset = e.position
		}
	}

	if e.wrapper != "" && line == "</"+e.wrapper+">" {
		e.document.ContentLength = start - e.document.ContentOffset
		e.contentEnded = true
	}

	if strings.Contains(line, "http://www.xbrl.org/2013/inlineXBRL") || strings.Contains(line, "http://www.xbrl.org/2008/inlineXBRL") {
		e.document.Role = "inline_xbrl"
	}

	return nil
}

func readHeaderField(metadata *FilingMetadata, line string) {
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return
	}

	value = strings.TrimSpace(value)

	switch key {
	case "ACCESSION NUMBER":
		metadata.Accession = value
	case "CONFORMED SUBMISSION TYPE":
		metadata.FormType = value
	case "FILED AS OF DATE":
		metadata.FilingDate = value
	case "CONFORMED PERIOD OF REPORT":
		metadata.ReportDate = value
	case "ITEM INFORMATION":
		metadata.Items = append(metadata.Items, value)
	case "CENTRAL INDEX KEY":
		if metadata.CIK == "" {
			metadata.CIK = value
		}
	}
}

func (f *ParsedFiling) diagnostic(code, message, document string) {
	f.Diagnostics = append(f.Diagnostics, ParseDiagnostic{Code: code, Message: message, Document: document})
}

func (f *ParsedFiling) extractInstances(ctx context.Context, source io.ReaderAt) error {
	inline, partial := false, false

	for index := range f.Documents {
		if err := ctx.Err(); err != nil {
			return err
		}

		document := &f.Documents[index]
		if document.Role == "inline_xbrl" {
			inline = true
		}

		if !strings.EqualFold(filepath.Ext(document.Filename), ".xml") && document.Type != "EX-101.INS" {
			continue
		}

		reader := io.NewSectionReader(source, document.ContentOffset, document.ContentLength)

		instance, recognized, err := readXBRL(ctx, reader)
		if err != nil {
			partial = true

			f.diagnostic("xbrl_error", err.Error(), document.Filename)

			continue
		}

		if !recognized {
			if document.Type == "EX-101.INS" || strings.HasSuffix(document.Filename, "_htm.xml") {
				partial = true

				f.diagnostic("xbrl_error", "expected instance has no XBRL root", document.Filename)
			}

			continue
		}

		document.Role = "xbrl_instance"
		instance.DocumentIndex, instance.Document = index, document.Filename

		f.Instances = append(f.Instances, instance)
		for _, message := range instance.referenceErrors() {
			partial = true

			f.diagnostic("xbrl_reference", message, document.Filename)
		}

		if len(instance.Unsupported) > 0 {
			partial = true

			f.diagnostic("unsupported_xbrl", "unrecognized instance elements retained; tuple interpretation is not supported", document.Filename)
		}
	}

	switch {
	case partial:
		f.Status = ParsePartial
	case len(f.Instances) > 0:
		f.Status = ParseComplete
	case inline:
		f.Status = ParseUnsupported
		f.diagnostic("inline_only", "inline XBRL conversion is not implemented; no extracted instance found", "")
	}

	return ctx.Err()
}

func readFilers(header []string) []SubmissionFiler {
	var result []SubmissionFiler

	active := false

	for _, raw := range header {
		line := strings.TrimSpace(raw)
		if line == "FILER:" {
			var filer SubmissionFiler

			result = append(result, filer)
			active = true

			continue
		}

		if raw == line && strings.HasSuffix(line, ":") {
			active = false
		}

		if !active {
			continue
		}

		filer := &result[len(result)-1]
		filer.Header = append(filer.Header, raw)

		key, value, _ := strings.Cut(line, ":")
		switch key {
		case "CENTRAL INDEX KEY":
			filer.CIK = strings.TrimSpace(value)
		case "COMPANY CONFORMED NAME":
			filer.Name = strings.TrimSpace(value)
		}
	}

	return result
}
