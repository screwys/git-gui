"""Integrate remote commits before a normal push."""
import argparse
import subprocess


def push(branch, skip_hook=False):
    subprocess.run(["git", "fetch", "origin"], check=True)
    remote = f"refs/remotes/origin/{branch}"
    if subprocess.run(["git", "show-ref", "--verify", "--quiet", remote]).returncode == 0:
        if subprocess.run(["git", "merge-base", "--is-ancestor", remote, "HEAD"]).returncode != 0:
            print(f"Applying local commits on top of origin/{branch}...", flush=True)
            subprocess.run(["git", "rebase", "--rebase-merges", "--autostash", remote], check=True)
            if subprocess.check_output(["git", "diff", "--name-only", "--diff-filter=U"]).strip():
                raise RuntimeError("Restoring local changes caused conflicts. Resolve them before pushing.")
    command = ["git", "push"]
    if skip_hook:
        command.append("--no-verify")
    subprocess.run([*command, "origin", branch], check=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("branch")
    parser.add_argument("--skip-hook", action="store_true")
    args = parser.parse_args()
    try:
        push(args.branch, args.skip_hook)
    except (subprocess.CalledProcessError, RuntimeError) as error:
        print(f"Push stopped: {error}", flush=True)
        print("Your local commits are retained. If a rebase is in progress, resolve conflicts and run "
              "'git rebase --continue', or use 'git rebase --abort'. Then retry Push.", flush=True)
        raise SystemExit(1)
