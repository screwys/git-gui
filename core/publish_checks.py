"""Prepare pending commits and run their pre-push checks without publishing."""
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

CHECK_INPUT = "Checked PR inputs: "


def git(repo, *args, env=None):
    return subprocess.check_output(["git", *args], cwd=repo, env=env, text=True).removesuffix("\n")


def pending_inputs(repo, branch, paths):
    """Read selected contents without changing the user's index."""
    head = git(repo, "rev-parse", "HEAD")
    branch_head = subprocess.run(
        ["git", "rev-parse", "--verify", f"refs/heads/{branch}"], cwd=repo,
        capture_output=True, text=True, check=False,
    ).stdout.strip()
    with tempfile.TemporaryDirectory(prefix="git-gui-index-") as directory:
        index = Path(directory) / "index"
        source_index = Path(git(repo, "rev-parse", "--path-format=absolute", "--git-path", "index"))
        if source_index.exists():
            # Keep Git's stat cache so unchanged files do not need hashing.
            shutil.copyfile(source_index, index)
        env = dict(os.environ, GIT_INDEX_FILE=str(index))
        git(repo, "read-tree", "--reset", head, env=env)
        if paths is None:
            git(repo, "add", "-A", "--", ".", env=env)
        elif paths:
            matched = git(repo, "--literal-pathspecs", "ls-files", "-z", "--cached", "--others",
                          "--exclude-standard", "--", *paths, env=env).rstrip("\0").split("\0")
            if matched != [""]:
                git(repo, "--literal-pathspecs", "add", "-A", "--", *dict.fromkeys(matched), env=env)
        tree = git(repo, "write-tree", env=env)
    return [head, branch_head, tree]


def has_pending_edits(repo, inputs):
    return inputs[2] != git(repo, "rev-parse", f"{inputs[0]}^{{tree}}")


def run_checks(repo, script, pending):
    branch = pending["branch"]
    while True:
        inputs = pending_inputs(repo, branch, pending["paths"])
        edited = has_pending_edits(repo, inputs)
        args = pending["args"] if edited else pending["retry_args"]
        if edited and args == pending["retry_args"]:
            # A clean branch can gain edits after publication was scheduled.
            subject = git(repo, "log", "-1", "--format=%s", f"refs/heads/{branch}")
            args = [*args, pending["title"] or subject]
        args = list(args)
        if "--branch" in args:
            args[args.index("--branch") + 1] = branch
        prepared_repo = None
        process = subprocess.Popen(
            [script, "--prepare-only", *args], cwd=repo, stdout=subprocess.PIPE,
            text=True, bufsize=1,
        )
        for line in process.stdout:
            print(line, end="", flush=True)
            if line.startswith("Prepared PR worktree: "):
                prepared_repo = line.removeprefix("Prepared PR worktree: ").rstrip("\n")
            elif line.startswith("Publishing branch: "):
                branch = line.removeprefix("Publishing branch: ").rstrip("\n")
        if process.wait() != 0:
            return process.returncode
        if prepared_repo is None:
            raise RuntimeError("The publish helper did not report a prepared worktree.")
        inputs = pending_inputs(repo, branch, pending["paths"])
        if not has_pending_edits(repo, inputs):
            break
        print("Pending changes edited during preparation. Preparing them again.", flush=True)
    print(CHECK_INPUT + json.dumps(inputs), flush=True)
    remote = git(prepared_repo, "remote", "get-url", "origin")
    head = git(prepared_repo, "rev-parse", "HEAD")
    remote_head = git(prepared_repo, "ls-remote", "origin", f"refs/heads/{branch}").split()
    old_head = remote_head[0] if remote_head else "0" * len(head)
    env = dict(os.environ, RUFIN_SKIP_PRE_PUSH_HOOK="0", RUFIN_SKIP_CLIPPY_HOOK="0")
    return subprocess.run(
        ["git", "hook", "run", "--ignore-missing", "pre-push", "--", "origin", remote],
        cwd=prepared_repo, env=env, text=True,
        input=f"refs/heads/{branch} {head} refs/heads/{branch} {old_head}\n",
        check=False,
    ).returncode


if __name__ == "__main__":
    try:
        raise SystemExit(run_checks(os.getcwd(), sys.argv[1], json.loads(sys.argv[2])))
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"Checks stopped: {error}", flush=True)
        raise SystemExit(1)
