package edgar

import (
	"bytes"
	"context"
	"fmt"
	"slices"
)

const (
	legacyMissingEntitySemicolonCode = "legacy_xbrl_missing_entity_semicolon"
	legacySplitClosingTagCode        = "legacy_xbrl_split_closing_tag"
	legacySplitEntityCode            = "legacy_xbrl_split_entity"
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
	if retryErr != nil {
		return XBRLInstance{}, retryRecognized, diagnostics, retryErr
	}

	if !retryRecognized {
		return XBRLInstance{}, retryRecognized, diagnostics, err
	}

	return instance, retryRecognized, diagnostics, nil
}

func normalizeLegacyXBRLCopy(source []byte, document string) ([]byte, []ParseDiagnostic) {
	// Compatibility rules always work on an owned copy and are only used after
	// the strict XML parser fails. The pristine SEC source bytes, offsets, and
	// checksum remain the audit trail.
	normalized := append([]byte(nil), source...)
	diagnostics := make([]ParseDiagnostic, 0, 4)

	normalized, diagnostics = normalizeLegacySplitEntities(normalized, document, diagnostics)
	normalized, diagnostics = normalizeLegacySplitClosingTags(normalized, document, diagnostics)
	normalized, diagnostics = normalizeLegacyEntity(normalized, "lt", document, diagnostics)
	normalized, diagnostics = normalizeLegacyEntity(normalized, "gt", document, diagnostics)
	normalized, diagnostics = normalizeLegacyTruncatedClosingTag(normalized, document, diagnostics,
		[]byte("</DescriptionOfDefinedContributionPensionAndO>"),
		legacyStrayClosingTagCode,
		"escaped known legacy truncated closing tag text")

	return normalized, diagnostics
}

func normalizeLegacySplitEntities(source []byte, document string, diagnostics []ParseDiagnostic) ([]byte, []ParseDiagnostic) {
	normalized := make([]byte, 0, len(source))
	replacements := 0

	for len(source) > 0 {
		name, consumed, ok := matchLegacySplitEntity(source)
		if ok {
			normalized = append(normalized, '&')
			normalized = append(normalized, name...)
			normalized = append(normalized, ';')
			source = source[consumed:]
			replacements++

			continue
		}

		normalized = append(normalized, source[0])
		source = source[1:]
	}

	if replacements == 0 {
		return normalized, diagnostics
	}

	return normalized, append(diagnostics, legacyDiagnostic(
		legacySplitEntityCode,
		"compacted split XML entity reference in parser-only copy",
		replacements,
		document,
	))
}

func matchLegacySplitEntity(source []byte) ([]byte, int, bool) {
	if len(source) == 0 || source[0] != '&' {
		return nil, 0, false
	}

	for _, name := range [][]byte{[]byte("lt"), []byte("gt"), []byte("amp"), []byte("quot"), []byte("apos")} {
		position, spaced := 1, false

		for _, char := range name {
			for position < len(source) && legacyWhitespace(source[position]) {
				position++
				spaced = true
			}

			if position >= len(source) || source[position] != char {
				position = 0

				break
			}

			position++
		}

		for position > 0 && position < len(source) && legacyWhitespace(source[position]) {
			position++
			spaced = true
		}

		if spaced && position < len(source) && source[position] == ';' {
			return name, position + 1, true
		}
	}

	return nil, 0, false
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
	if legacyWhitespace(value) {
		return true
	}

	return value == '<'
}

func legacyWhitespace(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func normalizeLegacySplitClosingTags(source []byte, document string, diagnostics []ParseDiagnostic) ([]byte, []ParseDiagnostic) {
	normalized := append([]byte(nil), source...)
	replacements, cursor := 0, 0

	for cursor < len(normalized) {
		start := bytes.IndexByte(normalized[cursor:], '<')
		if start < 0 {
			break
		}

		start += cursor

		slash := start + 1
		for slash < len(normalized) && legacyWhitespace(normalized[slash]) {
			slash++
		}

		if slash >= len(normalized) || normalized[slash] != '/' {
			cursor = start + 1

			continue
		}

		end := bytes.IndexByte(normalized[slash:], '>')
		if end < 0 {
			break
		}

		end += slash

		rawName := normalized[slash+1 : end]
		if slash == start+1 && !containsLegacyWhitespace(rawName) {
			cursor = end + 1

			continue
		}

		compactName := compactLegacyWhitespace(rawName)

		openName, ok := nearestOpenTagName(normalized[:start])
		if !ok || !bytes.Equal(openName, compactName) {
			cursor = end + 1

			continue
		}

		replacement := make([]byte, 0, len(compactName)+3)
		replacement = append(replacement, []byte("</")...)
		replacement = append(replacement, compactName...)
		replacement = append(replacement, '>')

		next := make([]byte, 0, len(normalized)-(end-start+1)+len(replacement))
		next = append(next, normalized[:start]...)
		next = append(next, replacement...)
		next = append(next, normalized[end+1:]...)
		normalized = next
		replacements++
		cursor = start + len(replacement)
	}

	if replacements == 0 {
		return source, diagnostics
	}

	return normalized, append(diagnostics, legacyDiagnostic(
		legacySplitClosingTagCode,
		"compacted split XML closing tag name in parser-only copy",
		replacements,
		document,
	))
}

func containsLegacyWhitespace(source []byte) bool {
	return slices.ContainsFunc(source, legacyWhitespace)
}

func compactLegacyWhitespace(source []byte) []byte {
	result := make([]byte, 0, len(source))
	for _, value := range source {
		if !legacyWhitespace(value) {
			result = append(result, value)
		}
	}

	return result
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
