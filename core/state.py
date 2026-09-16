import json
import os
from typing import Dict, Any, Optional

STATE_DIR = os.path.expanduser("~/.config/git-gui")
STATE_FILE = os.path.join(STATE_DIR, "state.json")


class StateManager:
    _cached_state: Optional[Dict[str, Any]] = None

    @classmethod
    def load(cls) -> Dict[str, Any]:
        if cls._cached_state is not None:
            return cls._cached_state

        if not os.path.exists(STATE_FILE):
            cls._cached_state = cls._default_state()
            cls.save(cls._cached_state)
            return cls._cached_state

        try:
            with open(STATE_FILE, "r", encoding="utf-8") as f:
                data = json.load(f)
                defaults = cls._default_state()
                defaults.update(data)
                cls._cached_state = defaults
                return cls._cached_state
        except Exception as e:
            print(f"Error loading state: {e}")
            cls._cached_state = cls._default_state()
            return cls._cached_state

    @classmethod
    def save(cls, state: Dict[str, Any]):
        cls._cached_state = state
        try:
            os.makedirs(STATE_DIR, exist_ok=True)
            with open(STATE_FILE, "w", encoding="utf-8") as f:
                json.dump(state, f, indent=2)
        except Exception as e:
            print(f"Error saving state: {e}")

    @classmethod
    def update(cls, **kwargs):
        current = cls.load()
        current.update(kwargs)
        cls.save(current)

    @classmethod
    def get(cls, key: str, default: Any = None) -> Any:
        return cls.load().get(key, default)

    @classmethod
    def _default_state(cls) -> Dict[str, Any]:
        return {
            "selected_project": "Rufin",
            "rufin_subtab": "Publish PR",
            "igloo_subtab": "Commit & Push",
            "rufin_tracked_pr": None,  # {"number": "...", "title": "...", "url": "...", "state": "..."}
            "rufin_release_state": None,  # {"version": "...", "url": "...", "jobs": {...}}
            "igloo_release_state": None,  # {"version": "...", "url": "...", "jobs": {...}}
        }
