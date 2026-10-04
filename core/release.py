"""Create and push a release tag without changing the working tree."""
import argparse
import json
from pathlib import Path
import re
import subprocess
import time

import yaml


def release_workflows(repo_path):
    workflows = []
    directory = Path(repo_path) / ".github" / "workflows"
    for path in sorted(directory.glob("*")):
        if path.suffix not in (".yml", ".yaml"):
            continue
        try:
            workflow = yaml.safe_load(path.read_text(encoding="utf-8"))
        except (OSError, yaml.YAMLError):
            continue
        if not isinstance(workflow, dict):
            continue
        events = workflow.get("on", workflow.get(True, {}))
        push = events.get("push") if isinstance(events, dict) else None
        if not isinstance(push, dict) or not push.get("tags"):
            continue
        publishes = any(
            str(step.get("uses", "")).startswith("softprops/action-gh-release@") or
            re.search(r"\bgh\s+release\s+create\b", str(step.get("run", "")))
            for job in workflow.get("jobs", {}).values()
            for step in job.get("steps", [])
        )
        if publishes:
            workflows.append((path.name, str(workflow.get("name", path.stem))))
    return workflows


def publish_with_workflow(workflow, tag, commit, notes):
    print(f"Waiting for {workflow} for {tag}...", flush=True)
    deadline = time.monotonic() + 120
    while True:
        runs = json.loads(subprocess.check_output([
            "gh", "run", "list", "--workflow", workflow, "--branch", tag,
            "--event", "push", "--limit", "5", "--json", "databaseId,headSha,url,status,conclusion"
        ], text=True))
        run = next((entry for entry in runs if entry["headSha"] == commit), None)
        if run:
            break
        if time.monotonic() >= deadline:
            raise RuntimeError("No release workflow appeared. Check its tag filter in GitHub Actions, then retry.")
        time.sleep(3)
    print(f"Following {run['url']}", flush=True)
    run_id = run["databaseId"]
    while run["status"] != "completed":
        time.sleep(3)
        run = json.loads(subprocess.check_output([
            "gh", "api", f"repos/{{owner}}/{{repo}}/actions/runs/{run_id}"
        ], text=True))
    if run["conclusion"] != "success":
        raise RuntimeError(f"Release workflow ended with {run['conclusion']}. Check GitHub Actions before retrying.")
    subprocess.run(["gh", "release", "view", tag], check=True)
    if notes:
        subprocess.run(["gh", "release", "edit", tag, "--notes", notes], check=True)


def release(tag, target, notes, push=False, github=False, workflow=""):
    subprocess.run(["git", "check-ref-format", f"refs/tags/{tag}"], check=True)
    if tag.startswith("-"):
        raise RuntimeError("A tag cannot start with '-'.")
    commit = subprocess.check_output([
        "git", "rev-parse", "--verify", "--end-of-options", f"{target}^{{commit}}"
    ], text=True).strip()
    ref = f"refs/tags/{tag}"
    existing = subprocess.run(["git", "show-ref", "--verify", "--quiet", ref])
    if existing.returncode == 0:
        tagged_commit = subprocess.check_output([
            "git", "rev-parse", "--verify", f"{ref}^{{commit}}"
        ], text=True).strip()
        if tagged_commit != commit:
            raise RuntimeError(f"{tag} already points to another commit. Choose another tag.")
        print(f"Reusing {tag} at {commit}. Its annotation is unchanged.", flush=True)
    else:
        subprocess.run([
            "git", "tag", "--annotate", "--cleanup=verbatim", "--message", notes or tag,
            "--", tag, commit
        ], check=True)
        print(f"Created {tag} at {commit}.", flush=True)
    if push:
        subprocess.run(["git", "push", "origin", f"{ref}:{ref}"], check=True)
    if github:
        if not push:
            remote_refs = subprocess.check_output([
                "git", "ls-remote", "origin", ref, f"{ref}^{{}}"
            ], text=True)
            remote = dict(line.split()[::-1] for line in remote_refs.splitlines())
            remote_commit = remote.get(f"{ref}^{{}}", remote.get(ref))
            if remote_commit != commit:
                raise RuntimeError("Origin must have this tag at the selected commit. Push the tag or choose its commit.")
        if workflow:
            publish_with_workflow(workflow, tag, commit, notes)
        else:
            subprocess.run([
                "gh", "release", "create", tag, "--verify-tag", "--title", tag,
                "--notes", notes or tag
            ], check=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--tag", required=True)
    parser.add_argument("--target", required=True)
    parser.add_argument("--notes", default="")
    parser.add_argument("--push", action="store_true")
    parser.add_argument("--github", action="store_true")
    parser.add_argument("--workflow", default="")
    args = parser.parse_args()
    try:
        release(args.tag, args.target, args.notes, args.push, args.github, args.workflow)
    except (subprocess.CalledProcessError, RuntimeError, OSError) as error:
        print(f"Release stopped: {error}", flush=True)
        print("Existing tags are retained. Check the terminal and remote status before retrying.", flush=True)
        raise SystemExit(1)
