#!/usr/bin/env python3
"""otoxan_pr.py — CLI for managing pull requests in otoxan repositories.

Wraps `gh` (GitHub CLI) to provide a streamlined manual workflow for:
  - Listing open PRs
  - Creating PRs from the current branch
  - Checking PR status / CI checks
  - Merging PRs
  - Viewing PR details

Supports two repos:
  otoxan       — berryhill/otoxan (Go CLI core)
  workspace    — berryhill/otoxan-workspace (umbrella)

Usage:
    python3 scripts/otoxan_pr.py list [--repo otoxan|workspace] [--state open|closed|all]
    python3 scripts/otoxan_pr.py create [--repo otoxan|workspace] [--title TITLE] [--body BODY] [--draft] [--base BRANCH]
    python3 scripts/otoxan_pr.py status <PR_NUMBER> [--repo otoxan|workspace]
    python3 scripts/otoxan_pr.py checks <PR_NUMBER> [--repo otoxan|workspace]
    python3 scripts/otoxan_pr.py merge <PR_NUMBER> [--repo otoxan|workspace] [--squash] [--delete-branch]
    python3 scripts/otoxan_pr.py view <PR_NUMBER> [--repo otoxan|workspace]
    python3 scripts/otoxan_pr.py diff <PR_NUMBER> [--repo otoxan|workspace]

Requires: gh (GitHub CLI), authenticated as berryhill.
"""

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path

REPOS = {
    "otoxan": "berryhill/otoxan",
    "workspace": "berryhill/otoxan-workspace",
}

DEFAULT_REPO = "otoxan"


def run_gh(args: list[str], capture: bool = True) -> subprocess.CompletedProcess:
    """Run a gh command and return the result."""
    cmd = ["gh"] + args
    result = subprocess.run(
        cmd,
        capture_output=capture,
        text=True,
    )
    return result


def detect_repo_from_cwd() -> str:
    """Try to detect which repo we're in based on CWD."""
    cwd = Path.cwd().resolve()

    # Check if we're inside the otoxan Go product
    try:
        remote = subprocess.run(
            ["git", "remote", "get-url", "origin"],
            capture_output=True, text=True, cwd=str(cwd),
        )
        url = remote.stdout.strip()
        if "otoxan-workspace" in url:
            return "workspace"
        if "berryhill/otoxan" in url:
            return "otoxan"
    except Exception:
        pass

    # Fallback: check path components
    parts = cwd.parts
    if "otoxan-workspace" in parts:
        return "workspace"
    if "extensions" in parts or "browser" in parts:
        return "workspace"

    return DEFAULT_REPO


def get_repo_flag(repo: str) -> list[str]:
    """Return the --repo flag for gh."""
    full_name = REPOS.get(repo)
    if not full_name:
        print(f"ERROR: Unknown repo '{repo}'. Choose from: {', '.join(REPOS.keys())}")
        sys.exit(1)
    return ["--repo", full_name]


def cmd_list(args):
    """List pull requests."""
    repo = args.repo or detect_repo_from_cwd()
    state = args.state or "open"
    limit = args.limit or 20

    gh_args = ["pr", "list"] + get_repo_flag(repo)
    gh_args += ["--state", state, "--limit", str(limit)]

    if args.author:
        gh_args += ["--author", args.author]
    if args.label:
        gh_args += ["--label", args.label]
    if args.json_output:
        gh_args += ["--json", "number,title,headRefName,state,createdAt,author,additions,deletions,changedFiles"]

    result = run_gh(gh_args)
    if result.returncode != 0:
        print(f"ERROR: {result.stderr.strip()}")
        sys.exit(1)

    if args.json_output:
        try:
            prs = json.loads(result.stdout)
            for pr in prs:
                author = pr.get("author", {}).get("login", "?")
                print(f"  #{pr['number']}  {pr['title']}")
                print(f"         {pr['headRefName']} -> main | {pr['state']} | +{pr['additions']}/-{pr['deletions']} ({pr['changedFiles']} files)")
                print(f"         by {author} at {pr['createdAt']}")
                print()
        except (json.JSONDecodeError, KeyError):
            print(result.stdout)
    else:
        repo_name = REPOS[repo]
        print(f"PRs for {repo_name} (state={state}):\n")
        if result.stdout.strip():
            print(result.stdout)
        else:
            print("  (no PRs found)")
    print()


def cmd_create(args):
    """Create a pull request from the current branch."""
    repo = args.repo or detect_repo_from_cwd()
    base = args.base or "main"

    # Check for uncommitted changes
    status = subprocess.run(
        ["git", "status", "--porcelain"],
        capture_output=True, text=True,
    )
    if status.stdout.strip():
        print("WARNING: You have uncommitted changes:")
        for line in status.stdout.strip().split("\n")[:10]:
            print(f"  {line}")
        if len(status.stdout.strip().split("\n")) > 10:
            print(f"  ... and {len(status.stdout.strip().split(chr(10))) - 10} more")
        print()

    # Gather commit messages for title/body if not provided
    if not args.title:
        # Use first commit message on this branch vs base
        log = subprocess.run(
            ["git", "log", f"{base}..HEAD", "--pretty=format:%s"],
            capture_output=True, text=True,
        )
        commits = log.stdout.strip().split("\n") if log.stdout.strip() else []
        if commits:
            title = commits[0]
            if len(commits) > 1:
                body = "\n".join(f"- {c}" for c in commits[1:])
            else:
                body = ""
        else:
            title = input("PR title: ").strip()
            body = ""

        if not title:
            print("ERROR: No title provided and could not infer from commits.")
            sys.exit(1)
    else:
        title = args.title
        body = args.body or ""

    gh_args = ["pr", "create"] + get_repo_flag(repo)
    gh_args += ["--title", title]
    if body:
        gh_args += ["--body", body]
    if args.draft:
        gh_args.append("--draft")
    gh_args += ["--base", base]

    # Show what we're about to do
    print(f"Creating PR in {REPOS[repo]}:")
    print(f"  Title: {title}")
    if body:
        print(f"  Body:  {body[:100]}{'...' if len(body) > 100 else ''}")
    print(f"  Base:  {base}")
    print(f"  Draft: {args.draft}")
    print()

    if not args.yes:
        answer = input("Proceed? [y/N] ").strip().lower()
        if answer not in ("y", "yes"):
            print("Aborted.")
            return

    result = run_gh(gh_args)
    if result.returncode != 0:
        print(f"ERROR: {result.stderr.strip()}")
        sys.exit(1)

    url = result.stdout.strip()
    print(f"Created PR: {url}")


def cmd_status(args):
    """Show PR status and review state."""
    repo = args.repo or detect_repo_from_cwd()

    gh_args = ["pr", "view", str(args.pr_number)] + get_repo_flag(repo)
    gh_args += ["--json", "number,title,state,mergeable,reviewDecision,statusCheckRollup,headRefName,baseRefName,url,author"]

    result = run_gh(gh_args)
    if result.returncode != 0:
        print(f"ERROR: {result.stderr.strip()}")
        sys.exit(1)

    try:
        pr = json.loads(result.stdout)
    except json.JSONDecodeError:
        # Fallback to plain view
        result2 = run_gh(["pr", "view", str(args.pr_number)] + get_repo_flag(repo))
        print(result2.stdout)
        return

    author = pr.get("author", {}).get("login", "?")
    print(f"PR #{pr['number']}: {pr['title']}")
    print(f"  URL:    {pr['url']}")
    print(f"  Author: {author}")
    print(f"  Branch: {pr.get('headRefName', '?')} -> {pr.get('baseRefName', 'main')}")
    print(f"  State:  {pr['state']}")
    print(f"  Mergeable: {pr.get('mergeable', 'unknown')}")

    review = pr.get("reviewDecision", "NONE")
    print(f"  Review: {review}")

    checks = pr.get("statusCheckRollup", [])
    if checks:
        print(f"  Checks ({len(checks)}):")
        for check in checks:
            name = check.get("name", "?")
            status = check.get("status", "?")
            conclusion = check.get("conclusion", "")
            if conclusion:
                print(f"    {name}: {conclusion}")
            else:
                print(f"    {name}: {status}")
    else:
        print("  Checks: none")
    print()


def cmd_checks(args):
    """Watch CI checks for a PR."""
    repo = args.repo or detect_repo_from_cwd()

    gh_args = ["pr", "checks", str(args.pr_number)] + get_repo_flag(repo)

    if args.watch:
        # Poll every 30 seconds
        import time
        while True:
            os.system("clear")
            print(f"Checks for PR #{args.pr_number} in {REPOS[repo]} (polling every 30s, Ctrl+C to stop):\n")
            result = run_gh(gh_args)
            print(result.stdout if result.returncode == 0 else result.stderr)
            print(f"\nLast checked: {time.strftime('%H:%M:%S')}")
            try:
                time.sleep(30)
            except KeyboardInterrupt:
                print("\nStopped.")
                break
    else:
        result = run_gh(gh_args)
        if result.returncode != 0:
            print(f"ERROR: {result.stderr.strip()}")
            sys.exit(1)
        print(f"Checks for PR #{args.pr_number} in {REPOS[repo]}:\n")
        print(result.stdout)


def cmd_merge(args):
    """Merge a pull request."""
    repo = args.repo or detect_repo_from_cwd()

    # First show status
    gh_args = ["pr", "view", str(args.pr_number)] + get_repo_flag(repo)
    gh_args += ["--json", "number,title,state,mergeable,reviewDecision,headRefName"]
    result = run_gh(gh_args)

    if result.returncode != 0:
        print(f"ERROR: {result.stderr.strip()}")
        sys.exit(1)

    try:
        pr = json.loads(result.stdout)
        print(f"Merging PR #{pr['number']}: {pr['title']}")
        print(f"  Branch: {pr.get('headRefName', '?')}")
        print(f"  State:  {pr['state']}")
        print(f"  Mergeable: {pr.get('mergeable', 'unknown')}")
        print(f"  Review: {pr.get('reviewDecision', 'NONE')}")
        print()
    except json.JSONDecodeError:
        pass

    if not args.yes:
        answer = input("Merge this PR? [y/N] ").strip().lower()
        if answer not in ("y", "yes"):
            print("Aborted.")
            return

    merge_args = ["pr", "merge", str(args.pr_number)] + get_repo_flag(repo)

    if args.squash:
        merge_args.append("--squash")
    elif args.rebase:
        merge_args.append("--rebase")
    else:
        merge_args.append("--merge")

    if args.delete_branch:
        merge_args.append("--delete-branch")
    else:
        merge_args.append("--delete-branch=false")

    result = run_gh(merge_args)
    if result.returncode != 0:
        print(f"ERROR: {result.stderr.strip()}")
        sys.exit(1)

    print(result.stdout.strip())


def cmd_view(args):
    """View PR details in terminal or open in browser."""
    repo = args.repo or detect_repo_from_cwd()

    if args.web:
        result = run_gh(["pr", "view", str(args.pr_number)] + get_repo_flag(repo) + ["--web"])
        return

    gh_args = ["pr", "view", str(args.pr_number)] + get_repo_flag(repo)
    gh_args += ["--json", "number,title,body,state,author,headRefName,baseRefName,url,createdAt,additions,deletions,changedFiles,labels,assignees"]

    result = run_gh(gh_args)
    if result.returncode != 0:
        print(f"ERROR: {result.stderr.strip()}")
        sys.exit(1)

    try:
        pr = json.loads(result.stdout)
        author = pr.get("author", {}).get("login", "?")
        labels = ", ".join(l["name"] for l in pr.get("labels", []))
        assignees = ", ".join(a["login"] for a in pr.get("assignees", []))

        print(f"{'=' * 60}")
        print(f"PR #{pr['number']}: {pr['title']}")
        print(f"{'=' * 60}")
        print(f"  URL:       {pr['url']}")
        print(f"  Author:    {author}")
        print(f"  Created:   {pr.get('createdAt', '?')}")
        print(f"  Branch:    {pr.get('headRefName', '?')} -> {pr.get('baseRefName', 'main')}")
        print(f"  State:     {pr['state']}")
        print(f"  Changes:   +{pr.get('additions', 0)} -{pr.get('deletions', 0)} ({pr.get('changedFiles', 0)} files)")
        if labels:
            print(f"  Labels:    {labels}")
        if assignees:
            print(f"  Assignees: {assignees}")
        body = pr.get("body", "")
        if body:
            print(f"\n{'─' * 60}")
            # Truncate very long bodies
            if len(body) > 2000:
                print(body[:2000])
                print(f"\n... (truncated, {len(body) - 2000} more chars)")
            else:
                print(body)
        print(f"{'─' * 60}")
    except json.JSONDecodeError:
        # Fallback
        result2 = run_gh(["pr", "view", str(args.pr_number)] + get_repo_flag(repo))
        print(result2.stdout)
    print()


def cmd_diff(args):
    """Show the diff for a PR."""
    repo = args.repo or detect_repo_from_cwd()

    gh_args = ["pr", "diff", str(args.pr_number)] + get_repo_flag(repo)

    result = run_gh(gh_args)
    if result.returncode != 0:
        print(f"ERROR: {result.stderr.strip()}")
        sys.exit(1)

    # Pipe through a pager if output is large
    output = result.stdout
    if len(output) > 500 and sys.stdout.isatty():
        pager = os.environ.get("PAGER", "less -R")
        proc = subprocess.run(
            pager.split(),
            input=output, text=True,
        )
    else:
        print(output)


def build_parser():
    parser = argparse.ArgumentParser(
        description="otoxan PR CLI — manage pull requests in otoxan repositories",
        prog="otoxan_pr",
    )
    parser.add_argument(
        "--repo", "-r",
        choices=list(REPOS.keys()),
        help=f"Target repo (default: auto-detect from CWD, fallback: {DEFAULT_REPO})",
    )

    sub = parser.add_subparsers(dest="command", help="Available commands")

    # list
    p_list = sub.add_parser("list", aliases=["ls"], help="List pull requests")
    p_list.add_argument("--state", "-s", default="open", choices=["open", "closed", "all"], help="PR state filter")
    p_list.add_argument("--limit", "-n", type=int, default=20, help="Max PRs to list")
    p_list.add_argument("--author", "-a", help="Filter by author")
    p_list.add_argument("--label", "-l", help="Filter by label")
    p_list.add_argument("--json", dest="json_output", action="store_true", help="JSON output")

    # create
    p_create = sub.add_parser("create", aliases=["new"], help="Create a pull request")
    p_create.add_argument("--title", "-t", help="PR title (default: first commit message)")
    p_create.add_argument("--body", "-b", help="PR body")
    p_create.add_argument("--draft", "-d", action="store_true", help="Create as draft")
    p_create.add_argument("--base", help="Base branch (default: main)")
    p_create.add_argument("--yes", "-y", action="store_true", help="Skip confirmation")

    # status
    p_status = sub.add_parser("status", help="Show PR status and review state")
    p_status.add_argument("pr_number", type=int, help="PR number")

    # checks
    p_checks = sub.add_parser("checks", help="Show CI checks for a PR")
    p_checks.add_argument("pr_number", type=int, help="PR number")
    p_checks.add_argument("--watch", "-w", action="store_true", help="Poll checks every 30s")

    # merge
    p_merge = sub.add_parser("merge", help="Merge a pull request")
    p_merge.add_argument("pr_number", type=int, help="PR number")
    p_merge.add_argument("--squash", action="store_true", help="Squash merge")
    p_merge.add_argument("--rebase", action="store_true", help="Rebase merge")
    p_merge.add_argument("--delete-branch", action="store_true", help="Delete branch after merge")
    p_merge.add_argument("--yes", "-y", action="store_true", help="Skip confirmation")

    # view
    p_view = sub.add_parser("view", help="View PR details")
    p_view.add_argument("pr_number", type=int, help="PR number")
    p_view.add_argument("--web", "-w", action="store_true", help="Open in browser")

    # diff
    p_diff = sub.add_parser("diff", help="Show PR diff")
    p_diff.add_argument("pr_number", type=int, help="PR number")

    return parser


def main():
    parser = build_parser()
    args = parser.parse_args()

    if not args.command:
        parser.print_help()
        sys.exit(0)

    commands = {
        "list": cmd_list,
        "ls": cmd_list,
        "create": cmd_create,
        "new": cmd_create,
        "status": cmd_status,
        "checks": cmd_checks,
        "merge": cmd_merge,
        "view": cmd_view,
        "diff": cmd_diff,
    }

    handler = commands.get(args.command)
    if not handler:
        print(f"Unknown command: {args.command}")
        parser.print_help()
        sys.exit(1)

    handler(args)


if __name__ == "__main__":
    main()
