#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/release_notes.sh <version> [--prev <prev-version>] [--mode auto|create|edit] [--dry-run]

Examples:
  scripts/release_notes.sh v0.1.5
  scripts/release_notes.sh 0.1.6 --mode create
  scripts/release_notes.sh v0.1.5 --prev v0.1.4 --dry-run
EOF
}

if [[ $# -lt 1 ]]; then
  usage
  exit 1
fi

VERSION_RAW="$1"
shift

VERSION="$VERSION_RAW"
if [[ "$VERSION" != v* ]]; then
  VERSION="v$VERSION"
fi
VERSION_NO_V="${VERSION#v}"

PREV_TAG=""
MODE="auto"
DRY_RUN="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prev)
      PREV_TAG="${2:-}"
      shift 2
      ;;
    --mode)
      MODE="${2:-}"
      shift 2
      ;;
    --dry-run)
      DRY_RUN="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ "$MODE" != "auto" && "$MODE" != "create" && "$MODE" != "edit" ]]; then
  echo "Invalid --mode value: $MODE (expected auto|create|edit)" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

if [[ ! -f CHANGELOG.md ]]; then
  echo "CHANGELOG.md not found in repo root." >&2
  exit 1
fi

if ! command -v gh >/dev/null 2>&1; then
  echo "GitHub CLI (gh) is required." >&2
  exit 1
fi

if [[ -z "$PREV_TAG" ]]; then
  while IFS= read -r tag; do
    if [[ "$tag" != "$VERSION" ]]; then
      PREV_TAG="$tag"
      break
    fi
  done < <(git tag --list 'v*' --sort=-v:refname)
fi

if [[ -z "$PREV_TAG" ]]; then
  PREV_TAG="v0.0.0"
fi

SECTION="$(
  awk -v target="$VERSION_NO_V" '
    BEGIN { in_section=0 }
    $0 ~ ("^## \\[" target "\\]") { in_section=1; next }
    /^## \[/ && in_section==1 { exit }
    in_section==1 { print }
  ' CHANGELOG.md
)"

if [[ -z "${SECTION//[[:space:]]/}" ]]; then
  echo "Could not find changelog section for version [$VERSION_NO_V] in CHANGELOG.md." >&2
  exit 1
fi

HIGHLIGHTS="$(
  {
    echo "$SECTION" | awk '/^### Added/{inside=1; next} /^### /{if(inside) exit} /^- /{if(inside) print}'
    echo "$SECTION" | awk '/^### Changed/{inside=1; next} /^### /{if(inside) exit} /^- /{if(inside) print}'
  } | sed '/^[[:space:]]*$/d' | head -n 6
)"

if [[ -z "${HIGHLIGHTS//[[:space:]]/}" ]]; then
  HIGHLIGHTS="- Release updates documented in CHANGELOG."
fi

ENV_VARS="$(
  echo "$SECTION" | grep -oE '`GLM_[A-Z0-9_]+`' | sort -u || true
)"

HEADERS="$(
  echo "$SECTION" | grep -oE '`X-GLM-[A-Za-z0-9-]+`' | sort -u || true
)"

NOTES_FILE="$(mktemp)"
{
  echo "## Quick Read"
  echo "- **What changed:** This release adds the changes listed below from \`CHANGELOG.md\`."
  echo "- **Activation model:** Additive changes with backward-compatible defaults unless explicitly enabled."
  echo "- **Operator impact:** Review new env vars/headers before rollout."
  echo
  echo "## Highlights"
  echo "$HIGHLIGHTS"
  echo
  echo "## Operational Notes"
  if [[ -n "${ENV_VARS//[[:space:]]/}" ]]; then
    echo "- New env vars:"
    while IFS= read -r line; do
      [[ -n "$line" ]] && echo "  - $line"
    done <<< "$ENV_VARS"
  else
    echo "- New env vars: none."
  fi
  if [[ -n "${HEADERS//[[:space:]]/}" ]]; then
    echo "- New/updated headers:"
    while IFS= read -r line; do
      [[ -n "$line" ]] && echo "  - $line"
    done <<< "$HEADERS"
  else
    echo "- New/updated headers: none."
  fi
  echo "- Compatibility: OpenAI-compatible runtime contract remains additive."
  echo
  echo "## Links"
  echo "- Full changelog diff: https://github.com/cassianwolfe/G-LM/compare/${PREV_TAG}...${VERSION}"
} > "$NOTES_FILE"

if [[ "$DRY_RUN" == "true" ]]; then
  cat "$NOTES_FILE"
  rm -f "$NOTES_FILE"
  exit 0
fi

RELEASE_EXISTS="false"
if gh release view "$VERSION" >/dev/null 2>&1; then
  RELEASE_EXISTS="true"
fi

if [[ "$MODE" == "auto" ]]; then
  if [[ "$RELEASE_EXISTS" == "true" ]]; then
    MODE="edit"
  else
    MODE="create"
  fi
fi

if [[ "$MODE" == "edit" ]]; then
  gh release edit "$VERSION" --notes-file "$NOTES_FILE"
else
  gh release create "$VERSION" --title "$VERSION" --notes-file "$NOTES_FILE"
fi

echo "Release notes published for ${VERSION} (mode=${MODE})."
rm -f "$NOTES_FILE"
