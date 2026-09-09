#!/usr/bin/env python3
"""Post a PR review (summary + severity-labelled inline comments) as the gh-authenticated user.

Input JSON:
{
  "repo": "canusdev/sightpane",
  "pr": 872,
  "body": "review summary (markdown)",
  "comments": [
    {"path": "backend/store.go", "line": 95, "severity": "BLOCKER", "body": "..."},
    ...
  ]
}

Why a script: GitHub rejects the WHOLE review (422) if any inline comment anchors a line that
is not inside a diff hunk — any line of a new file is fine, but a modified file only accepts
lines its hunks touch. This parses the PR diff, folds un-anchorable comments into the summary,
pins commit_id to the head SHA, prefixes each comment with its label, and verifies afterwards.

Usage:
  post_review.py review.json --dry-run          # show anchors + folds, post nothing
  post_review.py review.json                    # post as a COMMENT review
  post_review.py review.json --request-changes  # post as REQUEST_CHANGES
  post_review.py --request-changes --repo O/R --pr N --body "..."   # blocking follow-up only
"""
from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys

LABELS = {"BLOCKER", "SHOULD FIX", "NIT"}
_ENV = {k: v for k, v in os.environ.items() if k != "GITHUB_TOKEN"}  # dummy token 401s


def gh(*args: str, stdin: str | None = None) -> str:
    r = subprocess.run(["gh", *args], env=_ENV, capture_output=True, text=True, input=stdin)
    if r.returncode:
        sys.exit(f"gh {' '.join(args[:3])}… failed:\n{r.stderr.strip()}")
    return r.stdout


def commentable_lines(diff: str) -> dict[str, set[int] | None]:
    """path -> set of RIGHT-side lines inside hunks, or None for a wholly new file."""
    out: dict[str, set[int] | None] = {}
    path, new_file = None, False
    for ln in diff.splitlines():
        if ln.startswith("diff --git"):
            path, new_file = ln.split(" b/", 1)[1], False
            out[path] = set()
        elif ln.startswith("new file mode"):
            new_file = True
            out[path] = None
        elif ln.startswith("@@") and path and not new_file:
            m = re.match(r"@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@", ln)
            start, count = int(m.group(1)), int(m.group(2) or 1)
            out[path].update(range(start, start + count))
    return out


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("input", nargs="?", help="review JSON (omit for a body-only follow-up)")
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--request-changes", action="store_true")
    ap.add_argument("--repo"); ap.add_argument("--pr", type=int); ap.add_argument("--body")
    a = ap.parse_args()

    spec = json.load(open(a.input)) if a.input else {}
    repo = a.repo or spec["repo"]
    pr = a.pr or spec["pr"]
    body = a.body or spec.get("body", "")
    comments = spec.get("comments", [])
    event = "REQUEST_CHANGES" if a.request_changes else "COMMENT"

    head = json.loads(gh("pr", "view", str(pr), "-R", repo, "--json", "headRefOid"))["headRefOid"]
    anchors = commentable_lines(gh("pr", "diff", str(pr), "-R", repo))

    inline, folded = [], []
    for c in comments:
        sev = c.get("severity", "").upper()
        if sev not in LABELS:
            sys.exit(f"{c['path']}:{c['line']}: severity must be one of {sorted(LABELS)}")
        text = f"**{sev}:** {c['body'].strip()}"
        ok_lines = anchors.get(c["path"], set())
        anchorable = c["path"] in anchors and (ok_lines is None or c["line"] in ok_lines)
        if anchorable:
            inline.append({"path": c["path"], "line": c["line"], "side": "RIGHT", "body": text})
        else:
            folded.append(f"- {c['path']}:{c['line']} — {text}")
        print(f"  {'inline' if anchorable else 'FOLDED'}  {sev:10s} {c['path']}:{c['line']}")

    if folded:
        body = body.rstrip() + "\n\nNot anchorable inline (lines outside this diff):\n" + "\n".join(folded)
    if event == "REQUEST_CHANGES" and not body.strip():
        sys.exit("REQUEST_CHANGES needs a body")

    print(f"\n{len(inline)} inline, {len(folded)} folded, event={event}, head={head[:8]}")
    if a.dry_run:
        print("\n--- body ---\n" + body)
        return

    payload = {"commit_id": head, "event": event, "body": body, "comments": inline}
    res = json.loads(gh("api", "-X", "POST", f"repos/{repo}/pulls/{pr}/reviews",
                        "--input", "-", stdin=json.dumps(payload)))
    print(f"posted review {res['id']} by {res['user']['login']} state={res['state']}\n{res['html_url']}")
    decision = json.loads(gh("pr", "view", str(pr), "-R", repo, "--json", "reviewDecision"))
    print(f"PR reviewDecision: {decision['reviewDecision'] or '(none)'}")


if __name__ == "__main__":
    main()
