import os
import subprocess
from dataclasses import dataclass
from typing import List, Optional, Tuple, Dict


@dataclass
class GitFileItem:
    path: str
    status_code: str  # e.g., 'M ', ' M', '??', ' D', 'A ', 'R '
    additions: int = 0
    deletions: int = 0
    is_untracked: bool = False
    is_staged: bool = False
    is_deleted: bool = False
    original_path: str | None = None

    @property
    def filename(self) -> str:
        return os.path.basename(self.path)

    @property
    def dirname(self) -> str:
        d = os.path.dirname(self.path)
        return d if d else "."

    @property
    def display_status(self) -> str:
        if self.is_untracked:
            return "U"
        if self.is_deleted:
            return "D"
        s = self.status_code.strip()
        if "A" in s:
            return "A"
        if "M" in s:
            return "M"
        if "R" in s:
            return "R"
        return s if s else "M"


@dataclass
class GitWorktree:
    path: str
    branch: str = ""
    head: str = ""
    bare: bool = False
    prunable: bool = False
    locked: bool = False


@dataclass(frozen=True)
class GitComparison:
    left: str
    right: str | None = None
    include_untracked: bool = False


@dataclass(frozen=True)
class GitCommit:
    oid: str
    short_oid: str
    date: str
    subject: str


class GitManager:
    def __init__(self, repo_path: str = os.path.expanduser("~/Projects/Rufin")):
        self.repo_path = repo_path

    def _run_git(self, args: List[str], check: bool = False) -> Tuple[int, str, str]:
        try:
            p = subprocess.run(
                ["git", "-C", self.repo_path] + args,
                capture_output=True,
                text=True,
                errors="replace",
                check=check,
            )
            return p.returncode, p.stdout, p.stderr
        except Exception as e:
            return 1, "", str(e)

    def is_git_repo(self) -> bool:
        code, out, _ = self._run_git(["rev-parse", "--is-inside-work-tree"])
        return code == 0 and "true" in out

    def get_current_branch(self) -> str:
        code, out, _ = self._run_git(["branch", "--show-current"])
        if code == 0 and out.strip():
            return out.strip()
        code, out, _ = self._run_git(["rev-parse", "--short", "HEAD"])
        return out.strip() if code == 0 else "unknown"

    def get_branches(self) -> Tuple[str, List[str]]:
        current = self.get_current_branch()
        code, out, _ = self._run_git(["branch", "--format=%(refname:short)"])
        branches = [b.strip() for b in out.splitlines() if b.strip()]
        return current, branches

    def _read_git(self, args: list[str]) -> str:
        code, out, err = self._run_git(args)
        if code != 0:
            raise RuntimeError(err.strip() or "Git command failed")
        return out

    def get_worktrees(self) -> list[GitWorktree]:
        out = self._read_git(["worktree", "list", "--porcelain", "-z"])
        worktrees = []
        current = None
        for field in out.split("\0"):
            key, _, value = field.partition(" ")
            if key == "worktree":
                current = GitWorktree(value)
                worktrees.append(current)
            elif current is not None:
                if key == "branch":
                    current.branch = value.removeprefix("refs/heads/")
                elif key == "HEAD":
                    current.head = value
                elif key in ("bare", "prunable", "locked"):
                    setattr(current, key, True)
        return worktrees

    def get_comparison_refs(self) -> list[str]:
        out = self._read_git([
            "for-each-ref", "--format=%(refname:short)",
            "refs/heads", "refs/remotes", "refs/tags",
        ])
        return out.splitlines()

    def get_default_base(self, branch: str) -> str:
        code, out, _ = self._run_git(["symbolic-ref", "--short", "refs/remotes/origin/HEAD"])
        refs = self.get_comparison_refs()
        for ref in (f"origin/{branch}", branch, out.strip() if code == 0 else ""):
            if ref and ref in refs:
                return ref
        return branch

    def resolve_commit(self, ref: str) -> str:
        return self._read_git([
            "rev-parse", "--verify", "--end-of-options", f"{ref}^{{commit}}",
        ]).strip()

    def working_comparison(self) -> GitComparison:
        code, out, _ = self._run_git(["rev-parse", "--verify", "HEAD"])
        left = out.strip() if code == 0 else self._read_git(["hash-object", "-t", "tree", "/dev/null"]).strip()
        return GitComparison(left, include_untracked=True)

    def branch_comparison(self, base: str, include_uncommitted: bool) -> GitComparison:
        base_oid = self.resolve_commit(base)
        head = self.resolve_commit("HEAD")
        left = self._read_git(["merge-base", base_oid, head]).strip()
        return GitComparison(left, None if include_uncommitted else head, include_uncommitted)

    def commit_comparison(self, ref: str) -> GitComparison:
        oid = self.resolve_commit(ref)
        parents = self._read_git(["rev-list", "--parents", "-n", "1", oid]).split()
        left = parents[1] if len(parents) > 1 else self._read_git(["hash-object", "-t", "tree", "/dev/null"]).strip()
        return GitComparison(left, oid)

    def get_commits(self, limit: int = 200) -> list[GitCommit]:
        code, out, err = self._run_git([
            "log", f"-{limit}", "--format=%H%x00%h%x00%cs%x00%s", "HEAD", "--",
        ])
        if code != 0:
            # A new repository has no history yet.
            if self._run_git(["rev-parse", "--verify", "HEAD"])[0] != 0:
                return []
            raise RuntimeError(err.strip() or "Could not load commits")
        return [GitCommit(*line.split("\0", 3)) for line in out.splitlines()]

    @staticmethod
    def _comparison_args(comparison: GitComparison) -> list[str]:
        return [comparison.left] + ([comparison.right] if comparison.right else [])

    def get_comparison_files(self, comparison: GitComparison) -> list[GitFileItem]:
        revisions = self._comparison_args(comparison)
        fields = self._read_git(["diff", "--name-status", "-z", "--find-renames", *revisions, "--"]).split("\0")
        items = []
        index = 0
        while index < len(fields) and fields[index]:
            status, path = fields[index:index + 2]
            index += 2
            original = None
            if status.startswith(("R", "C")):
                original, path = path, fields[index]
                index += 1
            items.append(GitFileItem(path, status, is_deleted=status == "D", original_path=original))

        fields = iter(self._read_git(["diff", "--numstat", "-z", "--find-renames", *revisions, "--"]).split("\0"))
        stats = {}
        for field in fields:
            if not field:
                continue
            adds, dels, path = field.split("\t", 2)
            if not path:
                next(fields)  # Original path of a rename.
                path = next(fields)
            stats[path] = (int(adds) if adds.isdigit() else 0, int(dels) if dels.isdigit() else 0)
        for item in items:
            item.additions, item.deletions = stats.get(item.path, (0, 0))

        if comparison.include_untracked:
            paths = self._read_git(["ls-files", "--others", "--exclude-standard", "-z"]).split("\0")
            for path in paths:
                if path:
                    try:
                        additions = self._count_text_lines(os.path.join(self.repo_path, path))
                    except OSError:
                        additions = 0
                    items.append(GitFileItem(path, "??", additions=additions, is_untracked=True))
        return items

    def get_comparison_diff(self, comparison: GitComparison, items: list[GitFileItem]) -> str:
        if not items:
            return ""
        paths = list(dict.fromkeys(
            path for item in items if not item.is_untracked
            for path in (item.original_path, item.path) if path is not None
        ))
        chunks = []
        if paths:
            chunks.append(self._read_git([
                "--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--find-renames",
                *self._comparison_args(comparison), "--", *paths,
            ]))
        if comparison.include_untracked:
            chunks.append(self._format_untracked_diff([item.path for item in items if item.is_untracked]))
        return "\n\n".join(chunk.strip() for chunk in chunks if chunk.strip())

    def get_pr_draft(self, branch: str) -> Dict[str, str]:
        draft = {}
        for field in ("title", "body"):
            code, out, _ = self._run_git([
                "config", "--local", "--get", f"branch.{branch}.rufin-pr-{field}"
            ])
            if code == 0:
                draft[field] = out.removesuffix("\n")
        return draft

    def get_commit_subject(self) -> str:
        code, out, _ = self._run_git(["log", "-1", "--format=%s"])
        return out.strip() if code == 0 else ""

    def get_latest_tag(self) -> str:
        code, out, _ = self._run_git(["describe", "--tags", "--abbrev=0"])
        if code == 0 and out.strip():
            return out.strip()
        return "v0.1.0"

    def fetch(self) -> Tuple[int, str]:
        code, out, err = self._run_git(["fetch", "--prune", "origin"])
        return code, (out + err).strip()

    def pull(self) -> Tuple[int, str]:
        code, out, err = self._run_git(["pull", "--ff-only"])
        return code, (out + err).strip()

    @staticmethod
    def _is_binary(stream) -> bool:
        # Git uses a NUL in the first 8000 bytes to detect binary content.
        sample = stream.read(8000)
        stream.seek(0)
        return b"\0" in sample

    @classmethod
    def _count_text_lines(cls, path: str) -> int:
        with open(path, "rb") as stream:
            if cls._is_binary(stream):
                return 0
            count = 0
            last = b""
            while chunk := stream.read(64 * 1024):
                count += chunk.count(b"\n")
                last = chunk[-1:]
            return count + int(bool(last) and last != b"\n")

    def get_status_files(self) -> List[GitFileItem]:
        code, out, _ = self._run_git(["status", "--porcelain=v1", "-uall"])
        if code != 0 or not out.strip():
            return []

        # Gather numstat for staged and unstaged
        stats: Dict[str, Tuple[int, int]] = {}
        _, numstat_out, _ = self._run_git(["diff", "--numstat"])
        for line in numstat_out.splitlines():
            parts = line.split("\t")
            if len(parts) >= 3:
                adds = int(parts[0]) if parts[0].isdigit() else 0
                dels = int(parts[1]) if parts[1].isdigit() else 0
                stats[parts[2]] = (adds, dels)

        _, cached_numstat_out, _ = self._run_git(["diff", "--cached", "--numstat"])
        for line in cached_numstat_out.splitlines():
            parts = line.split("\t")
            if len(parts) >= 3:
                adds = int(parts[0]) if parts[0].isdigit() else 0
                dels = int(parts[1]) if parts[1].isdigit() else 0
                prev = stats.get(parts[2], (0, 0))
                stats[parts[2]] = (prev[0] + adds, prev[1] + dels)

        items: List[GitFileItem] = []
        for line in out.splitlines():
            if len(line) < 4:
                continue
            status_code = line[:2]
            raw_path = line[3:].strip()
            if " -> " in raw_path:
                raw_path = raw_path.split(" -> ")[-1].strip()

            path = raw_path.strip('"')
            is_untracked = status_code == "??"
            is_deleted = "D" in status_code
            is_staged = status_code[0] != " " and status_code[0] != "?"

            additions, deletions = stats.get(path, (0, 0))
            if is_untracked and additions == 0:
                full_p = os.path.join(self.repo_path, path)
                if os.path.isfile(full_p):
                    try:
                        additions = self._count_text_lines(full_p)
                    except Exception:
                        additions = 0

            items.append(
                GitFileItem(
                    path=path,
                    status_code=status_code,
                    additions=additions,
                    deletions=deletions,
                    is_untracked=is_untracked,
                    is_staged=is_staged,
                    is_deleted=is_deleted,
                )
            )

        return items

    def _format_untracked_diff(self, rel_paths: List[str]) -> str:
        chunks = []
        for rel in rel_paths:
            full = os.path.join(self.repo_path, rel)
            if not os.path.isfile(full):
                continue
            try:
                with open(full, "rb") as f:
                    if self._is_binary(f):
                        chunks.append(
                            f"diff --git a/{rel} b/{rel}\n"
                            "new file mode 100644\n"
                            f"Binary files /dev/null and b/{rel} differ"
                        )
                        continue
                with open(full, "r", encoding="utf-8", errors="replace") as f:
                    lines = f.readlines()
                chunk = [
                    f"diff --git a/{rel} b/{rel}",
                    "new file mode 100644",
                    "--- /dev/null",
                    f"+++ b/{rel}",
                    f"@@ -0,0 +1,{len(lines)} @@",
                ]
                chunk.extend("+" + l.rstrip("\r\n") for l in lines)
                chunks.append("\n".join(chunk))
            except Exception:
                pass
        return "\n\n".join(chunks)

    def get_diff(self, paths: Optional[List[str]] = None) -> str:
        diff_chunks = []

        if paths:
            tracked_paths = []
            untracked_paths = []
            for p in paths:
                code, _, _ = self._run_git(["ls-files", "--error-unmatch", p])
                if code == 0:
                    tracked_paths.append(p)
                else:
                    untracked_paths.append(p)

            if tracked_paths:
                code, out, _ = self._run_git(["diff", "HEAD", "--"] + tracked_paths)
                if not out.strip():
                    code, out, _ = self._run_git(["diff", "--"] + tracked_paths)
                    code_c, out_c, _ = self._run_git(["diff", "--cached", "--"] + tracked_paths)
                    out = (out + "\n" + out_c).strip()
                if out.strip():
                    diff_chunks.append(out.strip())

            if untracked_paths:
                u_diff = self._format_untracked_diff(untracked_paths)
                if u_diff.strip():
                    diff_chunks.append(u_diff.strip())
        else:
            # Whole diff: tracked
            code, out, _ = self._run_git(["diff", "HEAD"])
            if not out.strip():
                code, out, _ = self._run_git(["diff"])
                code_c, out_c, _ = self._run_git(["diff", "--cached"])
                out = (out + "\n" + out_c).strip()
            if out.strip():
                diff_chunks.append(out.strip())

            # Untracked files
            code, u_out, _ = self._run_git(["ls-files", "--others", "--exclude-standard"])
            if code == 0 and u_out.strip():
                u_paths = [up.strip() for up in u_out.splitlines() if up.strip()]
                u_diff = self._format_untracked_diff(u_paths)
                if u_diff.strip():
                    diff_chunks.append(u_diff.strip())

        return "\n\n".join(diff_chunks)

    def get_tag_notes(self, tag_name: str) -> str:
        if not tag_name.startswith("refs/tags/"):
            tag_name = f"refs/tags/{tag_name}"
        code, stdout, _ = self._run_git(["cat-file", "tag", tag_name])
        if code != 0:
            return ""

        raw = stdout
        lines = raw.splitlines()
        msg_lines = []
        in_message = False

        for line in lines:
            if not in_message:
                if line == "":
                    in_message = True
                continue
            if line.startswith("-----BEGIN PGP SIGNATURE-----"):
                break
            if line.startswith("## Changelog"):
                break
            msg_lines.append(line)

        return "\n".join(msg_lines).strip()
