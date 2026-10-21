import os
import unittest
from types import SimpleNamespace
from unittest.mock import Mock

os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")

from PyQt6.QtWidgets import QApplication, QWidget

from core.runner import ProcessRunner
from main import MainWindow
from ui.components.activity_tabs import ActivityTabs
from ui.tabs.igloo_release_tab import IglooReleaseTab
from ui.tabs.release_tab import ReleaseTab


class ActivityRestoreTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = QApplication.instance() or QApplication([])

    def test_saved_git_failure_does_not_open_dock_but_running_command_does(self):
        dock = QWidget()
        self.addCleanup(dock.deleteLater)
        runner = ProcessRunner(dock)
        window = SimpleNamespace(
            runner=runner, git_dock=dock, status_bar=Mock(),
            _on_runner_output=Mock(), _on_runner_finished=Mock(),
        )
        MainWindow._wire_signals(window)
        runner.restored.emit({"running": False, "exit_code": 1})
        self.assertTrue(dock.isHidden())
        runner.restored.emit({"running": True})
        self.assertFalse(dock.isHidden())

    def test_release_restore_only_focuses_running_commands(self):
        for tab_class in (ReleaseTab, IglooReleaseTab):
            with self.subTest(tab=tab_class.__name__):
                tabs = ActivityTabs()
                self.addCleanup(tabs.deleteLater)
                jobs, terminal = QWidget(), QWidget()
                tabs.addTab(jobs, "Jobs")
                tabs.addTab(terminal, "Terminal")
                tab = SimpleNamespace(output_tabs=tabs, terminal=terminal)
                tab_class._on_activity_restored(tab, {"running": False, "exit_code": 1})
                self.assertIs(tabs.currentWidget(), jobs)
                tab_class._on_activity_restored(tab, {"running": True})
                self.assertIs(tabs.currentWidget(), terminal)

    def test_completed_release_logs_do_not_replace_remote_job_state(self):
        for tab_class in (ReleaseTab, IglooReleaseTab):
            with self.subTest(tab=tab_class.__name__):
                tab = SimpleNamespace(
                    runner=SimpleNamespace(replaying_output=True, is_running=False),
                    terminal=Mock(), jobs_view=Mock(),
                )
                tab_class._on_output_received(tab, "old release output\n")
                tab.terminal.append_output.assert_called_once_with("old release output\n")
                tab.jobs_view.process_output_chunk.assert_not_called()
                tab.runner.is_running = True
                tab_class._on_output_received(tab, "active release output\n")
                tab.jobs_view.process_output_chunk.assert_called_once_with("active release output\n")
