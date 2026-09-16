import os
import subprocess


def send_desktop_notification(title: str, body: str, urgency: str = "critical"):
    """
    Sends a desktop notification using notify-send.
    Removes LD_LIBRARY_PATH from env to prevent library version mismatches.
    """
    env = os.environ.copy()
    env.pop("LD_LIBRARY_PATH", None)
    try:
        subprocess.Popen(
            ["notify-send", "-u", urgency, "-a", "Git GUI", title, body],
            env=env,
        )
    except Exception as e:
        print(f"Failed to send desktop notification: {e}")
