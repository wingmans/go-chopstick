package edgar

import (
	"bytes"
	"context"
	"fmt"
)

const (
	legacyMissingEntitySemicolonCode = "legacy_xbrl_missing_entity_semicolon"
	legacyStrayClosingTagCode        = "legacy_xbrl_stray_closing_tag"
)

func readXBRLWithLegacyNormalization(ctx context.Context, source []byte, document string) (XBRLInstance, bool, []ParseDiagnostic, error) {
	instance, recognized, err := readXBRL(ctx, bytes.NewReader(source))
	if err == nil || !recognized {
		return instance, recognized, nil, err
	}

	normalized, diagnostics := normalizeLegacyXBRLCopy(source, document)
	if len(diagnostics) == 0 {
		return XBRLInstance{}, recognized, nil, err
	}

	instance, retryRecognized, retryErr := readXBRL(ctx, bytes.NewReader(normalized))
	if retryErr != nil || !retryRecognized {
		return XBRLInstance{}, recognized, nil, err
	}

	return instance, retryRecognized, diagnostics, nil
}

func normalizeLegacyXBRLCopy(source []byte, document string) ([]byte, []ParseDiagnostic) {
	// Compatibility rules always work on an owned copy and are only used after
	// the strict XML parser fails. The pristine SEC source bytes, offsets, and
	// checksum remain the audit trail.
	normalized := append([]byte(nil), source...)
	diagnostics := make([]ParseDiagnostic, 0, 3)

	normalized, diagnostics = normalizeLegacyEntity(normalized, "lt", document, diagnostics)
	normalized, diagnostics = normalizeLegacyEntity(normalized, "gt", document, diagnostics)
	normalized, diagnostics = normalizeLegacyTruncatedClosingTag(normalized, document, diagnostics,
		[]byte("</DescriptionOfDefinedContributionPensionAndO>"),
		legacyStrayClosingTagCode,
		"escaped known legacy truncated closing tag text")

	return normalized, diagnostics
}

func normalizeLegacyEntity(source []byte, name, document string, diagnostics []ParseDiagnostic) ([]byte, []ParseDiagnostic) {
	marker := []byte("&" + name)
	normalized := make([]byte, 0, len(source))
	replacements := 0

	for len(source) > 0 {
		if bytes.HasPrefix(source, marker) {
			next := len(marker)
			if next < len(source) && source[next] != ';' && legacyEntityBoundary(source[next]) {
				normalized = append(normalized, marker...)
				normalized = append(normalized, ';')
				source = source[next:]
				replacements++

				continue
			}
		}

		normalized = append(normalized, source[0])
		source = source[1:]
	}

	if replacements == 0 {
		return normalized, diagnostics
	}

	return normalized, append(diagnostics, legacyDiagnostic(
		legacyMissingEntitySemicolonCode,
		fmt.Sprintf("added missing semicolon to &%s entity reference in parser-only copy", name),
		replacements,
		document,
	))
}

func legacyEntityBoundary(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n', '<':
		return true
	default:
		return false
	}
}

func normalizeLegacyTruncatedClosingTag(
	source []byte,
	document string,
	diagnostics []ParseDiagnostic,
	token []byte,
	code string,
	message string,
) ([]byte, []ParseDiagnostic) {
	normalized := append([]byte(nil), source...)
	replacements := 0

	for {
		index := bytes.Index(normalized, token)
		if index < 0 {
			break
		}

		openName, ok := nearestOpenTagName(normalized[:index])
		if !ok {
			return source, diagnostics
		}

		// The legacy token is text that starts like a closing tag. Escaping it
		// preserves the visible text, then the normalized copy closes the fact
		// element that was open at the malformed token.
		replacement := make([]byte, 0, len(token)+len(openName)+12)
		replacement = append(replacement, []byte("&lt;/DescriptionOfDefinedContributionPensionAndO&gt;</")...)
		replacement = append(replacement, openName...)
		replacement = append(replacement, '>')

		next := make([]byte, 0, len(normalized)-len(token)+len(replacement))
		next = append(next, normalized[:index]...)
		next = append(next, replacement...)
		next = append(next, normalized[index+len(token):]...)
		normalized = next
		replacements++
	}

	if replacements == 0 {
		return source, diagnostics
	}

	return normalized, append(diagnostics, legacyDiagnostic(code, message, replacements, document))
}

func nearestOpenTagName(source []byte) ([]byte, bool) {
	for cursor := len(source); cursor > 0; {
		index := bytes.LastIndexByte(source[:cursor], '<')
		if index < 0 {
			return nil, false
		}

		if index+1 < len(source) && source[index+1] != '/' && source[index+1] != '?' && source[index+1] != '!' {
			end := index + 1
			for end < len(source) && !tagNameBoundary(source[end]) {
				end++
			}

			if end > index+1 {
				return append([]byte(nil), source[index+1:end]...), true
			}
		}

		cursor = index
	}

	return nil, false
}

func tagNameBoundary(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n', '/', '>':
		return true
	default:
		return false
	}
}

func legacyDiagnostic(code, message string, replacements int, document string) ParseDiagnostic {
	return ParseDiagnostic{
		Code:     code,
		Message:  fmt.Sprintf("%s (%d replacement(s)); raw source unchanged", message, replacements),
		Document: document,
	}
}
