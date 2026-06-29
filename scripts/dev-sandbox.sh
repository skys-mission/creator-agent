#!/usr/bin/env bash
# dev-sandbox.sh — one-shot isolated dev/test environment for creator-agent.
#
# Auto build + auto chdir + auto cleanup:
#   1. builds a fresh binary into a throwaway sandbox
#   2. runs the agent with HOME= and cwd= the sandbox, so config/sessions/memory
#      /history/trace all stay self-contained (your repo and ~/.creator untouched)
#   3. removes the whole sandbox on exit (normal, error, or Ctrl-C)
#
# The API key never lands on disk: it is read from the real config and injected
# via OPENAI_API_KEY; the sandbox copy of the config has api_key stripped.
#
# Usage:
#   scripts/dev-sandbox.sh                  # interactive TUI in a fresh sandbox
#   scripts/dev-sandbox.sh -- "your prompt" # headless one-shot in a fresh sandbox
#   make dev-sandbox                        # same, via Makefile
#
# Env:
#   CA_MODE  permission mode: default (default) | trust | auto | readonly | sandbox
#            (sandbox = OS-level sandbox-exec/bwrap + writes allowed inside only)
#   CA_KEEP  if set to a name, preserve the sandbox at ~/.cache/dev-sandboxes/<name>
#            instead of deleting on exit (to resume a session later)
#   CA_BIN   use this executable instead of compiling into the sandbox
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
REAL_HOME="${HOME:?}"
REAL_XDG="${XDG_CONFIG_HOME:-}"

MODE="${CA_MODE:-default}"
PMODE="$MODE"; [[ "$MODE" == "sandbox" ]] && PMODE="auto"

die() { printf 'dev-sandbox: %s\n' "$*" >&2; exit 1; }

# Match config.GlobalConfigPath(): XDG when set, else ~/.creator.
global_config_path() {
	if [[ -n "$REAL_XDG" ]]; then
		printf '%s/creator/config.toml' "${REAL_XDG%/}"
	else
		printf '%s/.creator/config.toml' "$REAL_HOME"
	fi
}

# Strip surrounding ASCII whitespace and optional matching quotes.
toml_unquote() {
	local v="$1"
	v="${v#"${v%%[![:space:]]*}"}"
	v="${v%"${v##*[![:space:]]}"}"
	case "$v" in
	\"*\") v="${v#\"}"; v="${v%\"}" ;;
	\'*\') v="${v#\'}"; v="${v%\'}" ;;
	esac
	printf '%s' "$v"
}

# Resolve api_key from the default profile (same precedence as config.Resolve).
extract_api_key() {
	local cfg="$1"
	local default="" profile="" in_target=0 key="" fallback=""
	while IFS= read -r line || [[ -n "$line" ]]; do
		[[ "$line" =~ ^[[:space:]]*# ]] && continue
		if [[ "$line" =~ ^[[:space:]]*default[[:space:]]*=[[:space:]]*(.+)$ ]]; then
			default="$(toml_unquote "${BASH_REMATCH[1]}")"
			continue
		fi
		if [[ "$line" =~ ^\[profiles\.([^]]+)\][[:space:]]*$ ]]; then
			profile="${BASH_REMATCH[1]}"
			in_target=0
			[[ -n "$default" && "$profile" == "$default" ]] && in_target=1
			continue
		fi
		if [[ "$line" =~ ^[[:space:]]*api_key[[:space:]]*=[[:space:]]*(.+)$ ]]; then
			key="$(toml_unquote "${BASH_REMATCH[1]}")"
			if (( in_target )) && [[ -n "$key" ]]; then
				printf '%s' "$key"
				return 0
			fi
			[[ -z "$fallback" && -n "$key" ]] && fallback="$key"
		fi
	done < "$cfg"
	[[ -n "$fallback" ]] && { printf '%s' "$fallback"; return 0; }
	return 1
}

# Always apply CA_MODE to [permissions]; when CA_MODE=sandbox, force [sandbox] for this run.
apply_dev_overrides() {
	local cfg="$1" pmode="$2" os_sandbox="$3" sandbox_dir="$4"
	local tmp
	tmp="$(mktemp)"
	awk -v pmode="$pmode" -v os_sandbox="$os_sandbox" -v sandbox_dir="$sandbox_dir" '
		function emit_permissions() {
			if (!perm_done) {
				print "[permissions]"
				print "mode = \"" pmode "\""
				perm_done = 1
			}
		}
		function emit_sandbox() {
			if (!sandbox_done && os_sandbox == "1") {
				print "[sandbox]"
				print "enabled = true"
				print "allow_dirs = [\"" sandbox_dir "\"]"
				sandbox_done = 1
			}
		}
		/^\[permissions\]/ {
			emit_permissions()
			in_perm = 1
			in_sandbox = 0
			next
		}
		/^\[sandbox\]/ {
			if (os_sandbox == "1") {
				emit_sandbox()
				in_sandbox = 1
				in_perm = 0
				next
			}
			in_sandbox = 1
			in_perm = 0
			print
			next
		}
		/^\[/ {
			in_perm = 0
			in_sandbox = 0
			print
			next
		}
		in_perm && /^[[:space:]]*mode[[:space:]]*=/ { next }
		in_sandbox && os_sandbox == "1" && /^[[:space:]]*(enabled|allow_dirs)[[:space:]]*=/ { next }
		{ print }
		END {
			if (!perm_done) {
				print ""
				emit_permissions()
			}
			if (!sandbox_done && os_sandbox == "1") {
				print ""
				emit_sandbox()
			}
		}
	' "$cfg" > "$tmp"
	mv "$tmp" "$cfg"
}

# Forward everything after a leading "--" (or all args) to the agent.
AGENT_ARGS=()
if [[ "${1:-}" == "--" ]]; then shift; AGENT_ARGS=("$@"); elif (($#)); then AGENT_ARGS=("$@"); fi

# Throwaway sandbox by default; CA_KEEP=<name> keeps it for reuse.
if [[ -n "${CA_KEEP:-}" ]]; then
	[[ "$CA_KEEP" =~ ^[A-Za-z0-9_.-]+$ ]] || die "invalid CA_KEEP name: $CA_KEEP (allowed: A-Z a-z 0-9 . - _)"
	SANDBOX="$REAL_HOME/.cache/dev-sandboxes/$CA_KEEP"
	KEEP=1
else
	_tmpdir="${TMPDIR:-/tmp}"; _tmpdir="${_tmpdir%/}"
	SANDBOX="$(mktemp -d "$_tmpdir/dev-sandbox.XXXXXX")"
	KEEP=0
fi
[[ "$KEEP" -eq 0 ]] && trap 'rm -rf "$SANDBOX"' EXIT

CFG_DIR="$SANDBOX/.creator"
CFG="$CFG_DIR/config.toml"
mkdir -p "$CFG_DIR"

# Locate the real config; copy it with api_key stripped. The key is injected via
# env below, so the sandbox holds no secret.
real_cfg="$(global_config_path)"
if [[ -f "$real_cfg" ]]; then
	sed -E 's/^[[:space:]]*api_key[[:space:]]*=.*/api_key = ""/' "$real_cfg" > "$CFG"
	if [[ -z "${OPENAI_API_KEY:-}" ]]; then
		KEY="$(extract_api_key "$real_cfg" || true)"
		[[ -n "$KEY" ]] && export OPENAI_API_KEY="$KEY"
	fi
elif [[ -n "${OPENAI_API_KEY:-}" ]]; then
	# No real config but a key is already in env: synthesize a permissions-only file.
	printf '[permissions]\nmode = "%s"\n' "$PMODE" > "$CFG"
else
	die "no config at $(global_config_path) and no OPENAI_API_KEY; run creator-agent setup first."
fi

os_sandbox=0; [[ "$MODE" == "sandbox" ]] && os_sandbox=1
apply_dev_overrides "$CFG" "$PMODE" "$os_sandbox" "$SANDBOX"

# Auto-compile: fresh binary into the sandbox, removed together with it. CA_BIN
# overrides the build with a prebuilt binary (fast iteration, or a stub for tests).
if [[ -n "${CA_BIN:-}" && -x "$CA_BIN" ]]; then
	BIN="$CA_BIN"
else
	BIN="$SANDBOX/.dev-bin"
	( cd "$PROJECT_ROOT" && go build -o "$BIN" ./cmd/creator-agent )
fi

printf 'dev-sandbox: %s (mode=%s, keep=%s)\n' "$SANDBOX" "$MODE" "$([[ $KEEP -eq 1 ]] && echo yes || echo no)" >&2
[[ "$KEEP" -eq 0 ]] && printf 'dev-sandbox: sandbox will be removed on exit\n' >&2

# Run: HOME= and cwd= the sandbox => every path under DataDir() is self-contained.
cd "$SANDBOX"
export HOME="$SANDBOX"
unset XDG_CONFIG_HOME
if ((${#AGENT_ARGS[@]})); then
	"$BIN" "${AGENT_ARGS[@]}"
else
	"$BIN"
fi
