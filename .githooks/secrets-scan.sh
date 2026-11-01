#!/usr/bin/env bash
# Credential check for this public repository. Used by the git hooks in this folder and by CI.
#
#   .githooks/secrets-scan.sh staged          # what is about to be committed (pre-commit)
#   .githooks/secrets-scan.sh range A..B      # commits about to be pushed (pre-push)
#   .githooks/secrets-scan.sh all             # every commit in the history (CI, manual)
#
# Two checks: (1) no credential-shaped file names (.env, private keys, …) are added;
# (2) no credential-shaped strings, via gitleaks, or via a built-in pattern list when gitleaks is not installed.
# Exit status 1 means "do not publish". A false positive is silenced with a `gitleaks:allow` comment on that line.
set -euo pipefail

mode="${1:-staged}"
range="${2:-}"

# File names that must never be tracked. .env.example (placeholders only) is allowed.
forbidden='(^|/)(\.env(\..*)?|.*\.(pem|key|p12|pfx|keystore)|id_(rsa|dsa|ecdsa|ed25519)|credentials.*\.json|service-account.*\.json)$'
allowed='(^|/)\.env\.example$'

case "$mode" in
staged) names=$(git diff --cached --name-only --diff-filter=ACR) ;;
range) names=$(git log --format= --name-only --diff-filter=ACR "$range" | sort -u) ;;
all) names=$(git log --all --format= --name-only --diff-filter=ACR | sort -u) ;;
*) echo "usage: $0 staged | range A..B | all" >&2; exit 2 ;;
esac

bad=$(printf '%s\n' "$names" | grep -E "$forbidden" | grep -vE "$allowed" || true)
if [ -n "$bad" ]; then
	echo "secrets-scan: credential-like files would be published:" >&2
	printf '  %s\n' $bad >&2
	exit 1
fi

if command -v gitleaks >/dev/null 2>&1; then
	case "$mode" in
	staged) gitleaks git --pre-commit --staged --redact --no-banner . ;;
	range) gitleaks git --log-opts="$range" --redact --no-banner . ;;
	all) gitleaks git --log-opts="--all" --redact --no-banner . ;;
	esac
	exit $?
fi

# Fallback when gitleaks is missing: the key formats this project can plausibly hold, matched on added lines only.
echo "secrets-scan: gitleaks not found; using the built-in patterns (install gitleaks for full coverage)" >&2
patterns='sk-ant-[A-Za-z0-9_-]{20,}|sk-(proj-|svcacct-)?[A-Za-z0-9_-]{32,}|AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{40,}|xox[abprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{35}|-----BEGIN [A-Z ]*PRIVATE KEY-----'
case "$mode" in
staged) diff=$(git diff --cached -U0) ;;
range) diff=$(git log -p -U0 --format= "$range") ;;
all) diff=$(git log --all -p -U0 --format=) ;;
esac
hits=$(printf '%s\n' "$diff" | grep -E '^\+' | grep -v 'gitleaks:allow' | grep -oE "$patterns" | sed -E 's/^(.{8}).*/\1…(redacted)/' || true)
if [ -n "$hits" ]; then
	echo "secrets-scan: credential-like strings found:" >&2
	printf '  %s\n' $hits >&2
	exit 1
fi
