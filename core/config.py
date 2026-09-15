import json
import os
from dataclasses import dataclass, asdict, fields
from typing import Optional


@dataclass
class AppConfig:
    repo_path: str = "/var/home/screwy/Projects/Rufin"
    publish_pr_script: str = ".local/scripts/publish-pr.sh"
    release_script: str = ".local/scripts/release-rufin.sh"
    default_base_branch: str = "main"
    default_auto_merge: bool = True
    default_wait_ci: bool = False
    wrap_diff: bool = True

    def resolve_publish_script(self, current_repo: Optional[str] = None) -> str:
        repo = current_repo or self.repo_path
        if os.path.isabs(self.publish_pr_script):
            return self.publish_pr_script
        return os.path.join(repo, self.publish_pr_script)

    def resolve_release_script(self, current_repo: Optional[str] = None) -> str:
        repo = current_repo or self.repo_path
        if os.path.isabs(self.release_script):
            return self.release_script
        return os.path.join(repo, self.release_script)


class ConfigManager:
    CONFIG_DIR = os.path.expanduser("~/.config/rufin-git-gui")
    CONFIG_FILE = os.path.join(CONFIG_DIR, "config.json")

    @classmethod
    def load(cls) -> AppConfig:
        if not os.path.exists(cls.CONFIG_FILE):
            cfg = AppConfig()
            cls.save(cfg)
            return cfg
        try:
            with open(cls.CONFIG_FILE, "r", encoding="utf-8") as f:
                data = json.load(f)
            # Only pass keys that belong to AppConfig
            valid_keys = {f.name for f in fields(AppConfig)}
            filtered = {k: v for k, v in data.items() if k in valid_keys}
            return AppConfig(**filtered)
        except Exception as e:
            print(f"Error loading config: {e}")
            return AppConfig()

    @classmethod
    def save(cls, config: AppConfig):
        try:
            os.makedirs(cls.CONFIG_DIR, exist_ok=True)
            with open(cls.CONFIG_FILE, "w", encoding="utf-8") as f:
                json.dump(asdict(config), f, indent=2)
        except Exception as e:
            print(f"Error saving config: {e}")
