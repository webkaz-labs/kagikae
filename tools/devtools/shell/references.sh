# shellcheck shell=bash
# Source from the inspected tree's root, with root naming the tool's module root,
# target naming the inspected tree, and portable selecting directory-link policy.
# The caller supplies fail(), which must accumulate failures, and must return a
# failing exit status after this file returns if any failures were recorded.
# Reference counters remain available for the caller's coverage checks.
root=${root:?caller module root required}
portable=${portable:?caller directory-link policy required}
# --- every link resolves, and every citation names a section its target declares ---
# One producer for both, because a link and a `§` citation are the same thing at this
# level — a reference from one document into another — so the walk, the pruned
# directories and the fence dialect are written once. tools/devtools/docrefs/parser.go's package
# comment is normative for what the two extractors read, why they are two and not one,
# and what each cannot see; the kinds it emits are `link` and `cite`.
#
# The extractor's exit status is read, which `done < <(… )` threw away. Measured: an
# extractor that emits part of the walk and then dies produced
# `ok — 13 docs in the Map, 75 links resolved` and exit 0, because the count stayed far
# above its floor — the "walk that collapsed" the product adapter's floors detect. The `|| fail` keeps that loud rather than letting `set -e` kill the script with
# no message, which is indistinguishable from a clean run to anything reading only the
# status.
if [ "$portable" -eq 1 ]; then
  refs=$(cd -- "$root" && GOWORK=off GOFLAGS='' GOCACHE=${GOCACHE:-${TMPDIR:-/tmp}/kae-gocache} go run ./tools/devtools/cmd/docrefs "$target") ||
    fail "the reference extractor exited non-zero, so the walk is incomplete"
else
  refs=$(GOCACHE=${GOCACHE:-${TMPDIR:-/tmp}/kae-gocache} go run ./tools/devtools/cmd/docrefs) ||
    fail "the reference extractor exited non-zero, so the walks below are truncated and their counts mean nothing"
fi

links_checked=0
sections_checked=0
sections_md=0
sections_go=0
while IFS=$'\t' read -r kind citing target verdict name; do
  if [ -z "$kind" ]; then
    continue
  fi
  case $kind in
  link)
    dest=${target%%#*}
    dest=${dest#<}
    dest=${dest%>}
    case $dest in
      '' | '#'*) continue ;;
      *:*) continue ;;   # scheme:… — mailto:, https:, etc.
      /*) continue ;;    # absolute path, not ours to resolve
    esac
    links_checked=$((links_checked + 1))
    # Parameter expansion, not `dirname`: this loop runs once per link, and forking a
    # process there was measured at ~2.1s of the ~2.3s this script took — 294 of its ~320
    # subprocesses. The selftest calls this script once per case, so it dominated there too.
    # Written as an `if` rather than `[ ... ] && ...` because AGENTS.md forbids that form
    # under `set -e`.
    md_dir=${citing%/*}
    if [ "$md_dir" = "$citing" ]; then
      md_dir=.
    fi
    # `-f`, not `-e`: a directory satisfies `-e`, and replacing `docs/PRODUCT.md` with a
    # directory of that name was measured reporting `ok — 12 docs in the Map` with a whole
    # required document gone. Every target in this repository resolves to a regular file
    # today (measured over all of them, none resolving to a directory or a symlink to one),
    # so the narrowing costs nothing; a link deliberately pointing at a directory would fail
    # loudly here and is the case to revisit this line for.
    if [ "$portable" -eq 1 ] && [ -d "$md_dir/$dest" ]; then
      continue
    fi
    if [ ! -f "$md_dir/$dest" ]; then
      fail "$citing link target does not exist: $target"
    fi
    ;;
  cite)
    # The link half resolves the *file* a citation points at and stops there, so a
    # citation naming a section the file has never had is invisible to it. One shipped:
    # docs/ROADMAP.md cited "§ Tier-1 tools", found by a reviewer.
    case $verdict in
      # A target outside the tree cannot be resolved from here; tools/devtools/docrefs/parser.go's package
      # comment says which ones those are and why. Not counted, so the floor bounds only the walk
      # this repository can actually check.
      external) continue ;;
      resolves) : ;;
      absent)
        fail "$citing cites $target § $name, which that file declares no section for"
        ;;
      # Fail-open was the shape here: only `absent` failed, so a typo in the extractor's
      # verdict string turned a real phantom into a pass. Measured — `absent` misspelled
      # `abesnt` printed `ok` with the shipped `§ Tier-1 tools` citation present.
      *) fail "unrecognised section verdict from the extractor: $verdict" ;;
    esac
    sections_checked=$((sections_checked + 1))
    case $citing in
      *.md) sections_md=$((sections_md + 1)) ;;
      *.go) sections_go=$((sections_go + 1)) ;;
    esac
    ;;
  # The same fail-open shape as the verdict default above, one level out: with two kinds
  # sharing a producer, a kind this script does not know is a whole walk going unchecked
  # while every count below stays plausible.
  *) fail "unrecognised reference kind from the extractor: $kind" ;;
  esac
done <<REFS
$refs
REFS
