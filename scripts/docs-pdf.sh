#!/usr/bin/env bash

set -Eeuo pipefail

## Script to generate PDF documentation for the EDGAR Project Guide using Pandoc and XeLaTeX
# Usage: ./scripts/docs-pdf.sh
# This will generate the PDF documentation and save it to the default location or the path specified by the OUTPUT environment variable.  
# Dependencies: pandoc, xelatex (BasicTeX)


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT="${OUTPUT:-$ROOT_DIR/edgar-project-guide.pdf}"
PANDOC_BIN="${PANDOC_BIN:-}"
PDF_ENGINE_BIN="${PDF_ENGINE_BIN:-}"

cd "$ROOT_DIR"

die() {
  echo "error: $*" >&2
  exit 1
}

find_command() {
  local name="$1"
  local candidate

  [[ -n "${!name:-}" && -x "${!name}" ]] && {
    printf '%s\n' "${!name}"
    return 0
  }

  candidate="$(command -v "$2" 2>/dev/null || true)"
  [[ -n "$candidate" ]] && {
    printf '%s\n' "$candidate"
    return 0
  }

  return 1
}

install_dependencies() {
  command -v brew >/dev/null 2>&1 || die \
    "Homebrew is required to install documentation dependencies"

  command -v pandoc >/dev/null 2>&1 || {
    echo "Installing pandoc with Homebrew"
    brew install pandoc
  }

  command -v xelatex >/dev/null 2>&1 || \
    [[ -x /Library/TeX/texbin/xelatex ]] || {
    echo "Installing BasicTeX with Homebrew"
    brew install --cask basictex
  }
}

install_dependencies

PANDOC_BIN="$(find_command PANDOC_BIN pandoc || true)"
[[ -n "$PANDOC_BIN" ]] || die "pandoc is unavailable"

[[ -n "$PDF_ENGINE_BIN" ]] || \
  PDF_ENGINE_BIN="$(command -v xelatex 2>/dev/null || true)"
[[ -n "$PDF_ENGINE_BIN" ]] || {
  [[ -x /Library/TeX/texbin/xelatex ]] && \
    PDF_ENGINE_BIN="/Library/TeX/texbin/xelatex"
}
[[ -n "$PDF_ENGINE_BIN" ]] || \
  die "xelatex is unavailable; restart the terminal after installing BasicTeX"

mkdir -p "$(dirname "$OUTPUT")"

"$PANDOC_BIN" \
  docs/index.md \
  docs/01-overview/*.md \
  docs/02-edgar/*.md \
  docs/03-forms/*.md \
  docs/04-parser/*.md \
  docs/05-taxonomy/*.md \
  docs/90-reference/*.md \
  --from=gfm \
  --toc \
  --toc-depth=2 \
  --number-sections \
  --pdf-engine="$PDF_ENGINE_BIN" \
  -V geometry:margin=25mm \
  -V papersize=a4 \
  -M title="EDGAR Project Guide" \
  -o "$OUTPUT"

echo "Created $OUTPUT"
