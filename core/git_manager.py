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


class GitManager:
    def __init__(self, repo_path: str = "/var/home/screwy/Projects/Rufin"):
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
                        with open(full_p, "rb") as f:
                            additions = sum(1 for _ in f)
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
