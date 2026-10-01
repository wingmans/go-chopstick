package edgar

import (
	"context"

	"wingman.com/fetch-ecb/internal/parser"
)

// Parser types and entry points remain available from edgar while callers
// migrate to the explicit parser package.
type (
	ParsedFiling       = parser.ParsedFiling
	FilingMetadata     = parser.FilingMetadata
	SubmissionFiler    = parser.SubmissionFiler
	SubmissionDocument = parser.SubmissionDocument
	ParseDiagnostic    = parser.ParseDiagnostic
	QName              = parser.QName
	XMLNode            = parser.XMLNode
	XMLAttribute       = parser.XMLAttribute
	XMLContent         = parser.XMLContent
	XBRLInstance       = parser.XBRLInstance
	Fact               = parser.Fact
	FactContext        = parser.FactContext
	FactDimension      = parser.FactDimension
	FactUnit           = parser.FactUnit
	SubmissionSource   = parser.SubmissionSource
)

const (
	ParsedSchemaVersion     = parser.ParsedSchemaVersion
	ParserVersion           = parser.ParserVersion
	ParseComplete           = parser.ParseComplete
	ParseNoXBRL             = parser.ParseNoXBRL
	ParseUnsupported        = parser.ParseUnsupported
	ParsePartial            = parser.ParsePartial
	DefaultParsedDirectory  = parser.DefaultParsedDirectory
	DefaultFilingsDirectory = parser.DefaultFilingsDirectory
)

func ParseSubmission(ctx context.Context, path string) (*ParsedFiling, error) {
	return parser.ParseSubmission(ctx, path)
}

func ParseSubmissionSource(ctx context.Context,
	source SubmissionSource) (*ParsedFiling, error) {
	return parser.ParseSubmissionSource(ctx, source)
}

func ReadSubmissionMetadata(ctx context.Context,
	path string) (FilingMetadata, error) {
	return parser.ReadSubmissionMetadata(ctx, path)
}

func ReadSubmissionMetadataSource(ctx context.Context,
	source SubmissionSource) (FilingMetadata, error) {
	return parser.ReadSubmissionMetadataSource(ctx, source)
}

func SaveParsedFiling(directory string, filing *ParsedFiling) (string, error) {
	return parser.SaveParsedFiling(directory, filing)
}

func LoadParsedFiling(path string) (*ParsedFiling, error) {
	return parser.LoadParsedFiling(path)
}
