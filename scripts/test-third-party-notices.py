#!/usr/bin/env python3
"""Offline failure controls for selected runtime notice reproduction."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('notices', Path(__file__).with_name('check-third-party-notices.py'))
notices = importlib.util.module_from_spec(spec)
spec.loader.exec_module(notices)


class NoticeTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.go_root = self.root / 'go'
        self.module_root = self.root / 'module'
        self.go_root.mkdir()
        self.module_root.mkdir()
        (self.go_root / 'LICENSE').write_text('Go license fixture\n')
        (self.module_root / 'LICENSE').write_text('Runtime copyright fixture\n')
        (self.root / 'Supplement.txt').write_text('Supplement license fixture\n')
        self.selection = {'version': 1, 'go_version': 'go1.27.3',
                          'targets': ['linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64'],
                          'modules': {'example.test/common': ['LICENSE'], 'example.test/darwin': ['LICENSE']},
                          'go_files': ['LICENSE'], 'supplements': [
                              {'title': 'Supplement', 'file': 'Supplement.txt', 'source': 'https://example.test/license'}]}
        self.sources = self.root / 'sources.json'
        self.version = 'v1.0.0'
        self.write_selection()
        self.calls = []

    def write_selection(self):
        self.sources.write_text(json.dumps(self.selection))

    def command(self, go, root, arguments, environment):
        if arguments == ['env', 'GOVERSION']:
            return 'go1.27.3\n'
        if arguments == ['env', 'GOROOT']:
            return str(self.go_root) + '\n'
        if arguments[:2] == ['list', '-m']:
            return 'agentmux\n'
        self.calls.append((environment['GOOS'], environment['GOARCH']))
        modules = ['example.test/common']
        if environment['GOOS'] == 'darwin':
            modules.append('example.test/darwin')
        return '\n'.join(json.dumps({'ImportPath': name, 'Module': {'Path': name,
                          'Version': self.version, 'Dir': str(self.module_root)}}) for name in modules)

    def generate(self):
        with mock.patch.object(notices, 'command', side_effect=self.command):
            return notices.generate(self.root, 'fixture-go', self.sources)

    def test_all_release_targets_and_exact_texts_are_reproduced_without_local_paths(self):
        data, modules, sections = self.generate()
        self.assertEqual((modules, sections), (2, 4))
        self.assertEqual(set(self.calls), {('linux', 'amd64'), ('linux', 'arm64'), ('darwin', 'amd64'), ('darwin', 'arm64')})
        self.assertIn(b'example.test/darwin v1.0.0', data)
        self.assertIn((self.module_root / 'LICENSE').read_bytes(), data)
        self.assertNotIn(str(self.root).encode(), data)

    def test_uncovered_target_specific_dependency_is_rejected(self):
        del self.selection['modules']['example.test/darwin']
        self.write_selection()
        with self.assertRaisesRegex(ValueError, 'lacks selected notices'):
            self.generate()

    def test_missing_or_duplicate_release_target_is_rejected(self):
        for targets in (['linux/amd64'], ['linux/amd64'] * 4):
            with self.subTest(targets=targets):
                self.selection['targets'] = targets
                self.write_selection()
                with self.assertRaisesRegex(ValueError, 'all four distinct release targets'):
                    self.generate()

    def test_missing_selected_notice_is_rejected(self):
        (self.module_root / 'LICENSE').unlink()
        with self.assertRaisesRegex(ValueError, 'missing regular selected notice'):
            self.generate()

    def test_default_check_refuses_notice_drift_without_rewriting(self):
        output = self.root / 'notice.txt'
        output.write_bytes(self.generate()[0])
        original = output.read_bytes()
        (self.module_root / 'LICENSE').write_text('Changed upstream copyright\n')
        with mock.patch.object(notices, 'command', side_effect=self.command), contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit) as error:
                notices.main(['--root', str(self.root), '--sources', str(self.sources), '--output', str(output)])
        self.assertEqual(error.exception.code, 1)
        self.assertEqual(output.read_bytes(), original)

    def test_dependency_version_changes_generated_evidence(self):
        original = self.generate()[0]
        self.version = 'v1.0.1'
        changed = self.generate()[0]
        self.assertNotEqual(changed, original)
        self.assertIn(b'example.test/common v1.0.1', changed)

    def test_write_is_explicit_and_go_version_is_pinned(self):
        output = self.root / 'notice.txt'
        with mock.patch.object(notices, 'command', side_effect=self.command), contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(notices.main(['--root', str(self.root), '--sources', str(self.sources), '--output', str(output), '--write']), 0)
            self.assertEqual(notices.main(['--root', str(self.root), '--sources', str(self.sources), '--output', str(output), '--check']), 0)
        self.selection['go_version'] = 'go1.24.0'
        self.write_selection()
        with self.assertRaisesRegex(ValueError, 'pinned release Go toolchain'):
            self.generate()


if __name__ == '__main__':
    unittest.main()
