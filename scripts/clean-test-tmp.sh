#!/usr/bin/env bash
# clean-test-tmp.sh — remove leftover test temp dirs/files from /tmp.
#
# The test suites create temp dirs via testutils.TempDir / TempDirMain, which
# register t.Cleanup / TestMain cleanup so a NORMAL run leaves nothing behind.
# The one historical deterministic leak — gorngit's group "<repoRoot>.allowed"
# sibling file written outside its TempDir — was fixed (it now registers an
# explicit t.Cleanup). Remaining /tmp garbage is therefore from KILLED runs
# (Ctrl-C, timeout, OOM, crash): when a test binary is killed, t.Cleanup never
# runs and its temp dirs stay in /tmp. This script reclaims that garbage.
#
# Safety:
#   - Only removes entries whose name starts with one of the known TEST
#     prefixes below. It never touches unrelated /tmp content.
#   - Long-running user nodes (e.g. /tmp/gogit-manual, gonomadnet daemons) do
#     NOT match any of these prefixes and are never touched.
#   - -n  dry run: list what would be removed, remove nothing.
#   - -m MINUTES  only remove entries older than MINUTES mtime (default: 0,
#     i.e. all). Use this to spare dirs from a currently-running test suite.
#
# Usage:
#   scripts/clean-test-tmp.sh            # remove all test garbage
#   scripts/clean-test-tmp.sh -n         # dry run (list only)
#   scripts/clean-test-tmp.sh -m 60      # remove only entries > 60 min old

set -euo pipefail

dry_run=0
min_minutes=0
while getopts "nm:" opt; do
  case "$opt" in
    n) dry_run=1 ;;
    m) min_minutes="$OPTARG" ;;
    *) echo "usage: $0 [-n] [-m MINUTES]" >&2; exit 2 ;;
  esac
done

# Known TEST temp-dir/file prefixes from go-reticulum and go-nomadnet suites.
# Keep these in sync with testutils.TempDir(...) prefixes in both repos.
#
# NOTE: bare "gogit-" is intentionally NOT listed — the user's long-running
# manual node lives at /tmp/gogit-manual and must never be touched. Only the
# specific gogit-remote-rns test-suite prefixes are listed (none of them match
# gogit-manual).
prefixes=(
  gorngit- gornx- gornsh- gornstatus- gorncp- gornodeconf-
  gornid- gornir- gornpath- gornpkg- gornprobe- gornsd- gorns-
  gogit-clone- gogit-remote-rns- gogit-seed- gogit-reclone-
  golxmd-test-
  rns-test-
  lxmf-int- nomadnet-rrc-int-test nomadnet-app-test nomadnet-config-test
  nomadnet-conversation-test nomadnet-directory-test nomadnet-dir-persist
  nomadnet-int- nomadnet-lxmf-xproc- nomadnet-node- nomadnet-peersettings-test
  nomadnet-rrc- nomadnet-storage-test nomadnet-cbor- gonomadnet-test-
  browser-cache-test- browser-download browser-fetch- browser-partial
  pipe-repeat-ts rns-local-parity- go-reticulum-large-py-to-go-
  probe_lxmf_store kiss-escape- rnstatus-parity-
  prettysize-parity- prettyspeed-parity-
)

removed=0
for p in "${prefixes[@]}"; do
  for f in /tmp/${p}*; do
    [ -e "$f" ] || [ -L "$f" ] || continue
    if [ "$min_minutes" -gt 0 ]; then
      # Skip entries modified within the last min_minutes minutes.
      age_min=$(( ($(date +%s) - $(stat -f %m "$f")) / 60 ))
      [ "$age_min" -lt "$min_minutes" ] && continue
    fi
    if [ "$dry_run" -eq 1 ]; then
      echo "DRY-RUN rm -rf $f"
    else
      rm -rf "$f"
    fi
    removed=$((removed + 1))
  done
done

# A couple of exact-name files that some suites drop in /tmp.
for f in /tmp/reticulum-phase-files.txt; do
  [ -e "$f" ] || continue
  if [ "$dry_run" -eq 1 ]; then echo "DRY-RUN rm -rf $f"; else rm -rf "$f"; fi
  removed=$((removed + 1))
done

if [ "$dry_run" -eq 1 ]; then
  echo "dry run: $removed entries would be removed"
else
  echo "removed $removed test entries from /tmp"
fi