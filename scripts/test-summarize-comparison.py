#!/usr/bin/env python3
"""Exercise comparison summaries with interrupted, contradictory and real evidence."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'scripts/summarize-comparison.py'
SPEC = importlib.util.spec_from_file_location('comparison_summary', SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def execution(passed=True, seconds=2, timed_out=False):
    return {'passed': passed, 'elapsed_seconds': seconds,
            'exit_code': 0 if passed else None if timed_out else 7,
            'timed_out': timed_out, 'error': None if passed else 'PRIVATE_RAW_ERROR',
            'cleanup_errors': []}


def grade(passed=True):
    return dict(execution(passed, 0.5), id='contract', artifact_intact=True,
                evaluator_intact=True, artifact_before_sha256='a' * 64,
                artifact_after_sha256='a' * 64)


def fixture():
    config = {'version': 1, 'mode': 'live', 'repeats': 1,
              'source': {'repository': '/PRIVATE_SOURCE_PATH', 'commit': '1' * 40},
              'tasks': [{'id': 'task', 'prompt': 'PRIVATE_TASK_PROMPT', 'checks': [{'id': 'contract'}]}],
              'arms': [{'id': 'baseline'}, {'id': 'agentmux'}]}
    attempts = []
    for position, arm in enumerate(('baseline', 'agentmux'), 1):
        attempts.append({'task': 'task', 'arm': arm, 'repeat': 1, 'position': position,
                         'completed': True, 'passed': True,
                         'execution': dict(execution(seconds=2 if position == 1 else 1), argv=['PRIVATE_COMMAND']),
                         'checks': [grade()]})
    report = {'version': 1, 'mode': 'live', 'planned_attempts': 2,
              'completed': True, 'passed': True, 'attempts': attempts, 'error': None,
              'source': copy.deepcopy(config['source']), 'snapshot_sha256': 'f' * 64,
              'arbitrary': 'PRIVATE_UNKNOWN_FIELD'}
    return config, report


class SummaryTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='amx-comparison-summary-')
        self.root = Path(self.temporary.name)
        self.config, self.report = fixture()

    def tearDown(self):
        self.temporary.cleanup()

    def write(self):
        raw = json.dumps(self.config).encode()
        (self.root / 'configuration.original.json').write_bytes(raw)
        self.report['configuration_sha256'] = hashlib.sha256(raw).hexdigest()
        (self.root / 'comparison.json').write_text(json.dumps(self.report))

    def summarize(self):
        self.write()
        return MODULE.summarize(self.root)

    def cli(self, *flags):
        return subprocess.run([sys.executable, str(SCRIPT), str(self.root), *flags],
                              capture_output=True, text=True, timeout=10)

    def test_pairs_are_matched_and_metadata_remains_unknown(self):
        result = self.summarize()
        self.assertEqual(result['pairs'][0]['both_passed'], 1)
        self.assertEqual(result['pairs'][0]['right_over_left_elapsed_both_passed'],
                         {'known': 1, 'min': 0.5, 'median': 0.5, 'max': 0.5})
        self.assertFalse(result['planned_order_balanced'])
        for field in ('human_interventions', 'inference_requests', 'tokens', 'cost'):
            self.assertIsNone(result[field])
        self.assertNotIn('winner', result)

    def test_actor_failure_cannot_be_rescued_by_good_artifact_or_excluded_from_denominator(self):
        actor = self.report['attempts'][0]
        actor.update(execution=execution(False), passed=False)
        self.report['passed'] = False
        result = self.summarize()
        self.assertEqual(result['arms'][0]['failed'], 1)
        self.assertEqual(result['arms'][0]['actor_failures'], 1)
        self.assertEqual(result['arms'][0]['failed_checks'], 0)
        pair = result['pairs'][0]
        self.assertEqual(pair['only_right_passed'], 1)
        self.assertEqual(pair['right_over_left_elapsed_both_passed']['known'], 0)

    def test_partial_and_unstarted_attempts_are_never_zero_failures_disguised_as_success(self):
        self.report['completed'] = self.report['passed'] = False
        self.report['attempts'] = [dict(self.report['attempts'][0], completed=False, passed=False, checks=[])]
        result = self.summarize()
        self.assertEqual(result['arms'][0]['incomplete'], 1)
        self.assertEqual(result['arms'][0]['failed'], 0)
        self.assertEqual(result['arms'][1]['not_started'], 1)
        self.assertEqual(result['pairs'][0]['incomplete_pairs'], 1)

    def test_timeouts_cleanup_and_zero_duration_are_explicit(self):
        self.report['attempts'][1]['execution'] = execution(False, timed_out=True)
        self.report['attempts'][1]['execution']['cleanup_errors'] = ['PRIVATE_CLEANUP']
        self.report['attempts'][1]['passed'] = self.report['passed'] = False
        result = self.summarize()
        self.assertEqual(result['arms'][1]['actor_timeouts'], 1)
        self.assertEqual(result['arms'][1]['cleanup_failures'], 1)
        self.report = fixture()[1]
        self.report['attempts'][0]['execution']['elapsed_seconds'] = 0
        result = self.summarize()
        self.assertEqual(result['pairs'][0]['both_passed'], 1)
        self.assertEqual(result['pairs'][0]['right_over_left_elapsed_both_passed']['known'], 0)

    def test_contradictory_evidence_is_refused(self):
        original = copy.deepcopy(self.report)
        mutations = (
            lambda r: r['attempts'][0].update(passed=False),
            lambda r: r['attempts'][0]['execution'].update(exit_code=9),
            lambda r: r['attempts'][0]['checks'][0].update(artifact_after_sha256='b' * 64),
            lambda r: r['attempts'][0]['checks'][0].update(evaluator_intact=False),
            lambda r: r['attempts'][0].update(position=2),
            lambda r: r['attempts'].append(copy.deepcopy(r['attempts'][0])),
            lambda r: r['attempts'][0].update(repeat=True),
            lambda r: r.update(planned_attempts=4),
            lambda r: r['attempts'][0]['execution'].update(elapsed_seconds=-1),
            lambda r: r['source'].update(commit='2' * 40),
            lambda r: r.update(snapshot_sha256='not-a-digest'),
        )
        for mutate in mutations:
            with self.subTest(mutation=mutate):
                self.report = copy.deepcopy(original)
                mutate(self.report)
                self.write()
                result = self.cli('--json')
                self.assertEqual(result.returncode, 2)
                self.assertEqual(result.stdout, '')
                self.assertNotIn('PRIVATE_', result.stderr)

    def test_frozen_config_and_strict_json_are_checked_without_echoing_private_input(self):
        self.write()
        path = self.root / 'comparison.json'
        original = path.read_text()
        cases = (original.replace('"version": 1', '"version": 1,"version": 1', 1),
                 original.replace('"elapsed_seconds": 2', '"elapsed_seconds": NaN', 1),
                 original[:-3], '{"PRIVATE_SECRET":')
        for raw in cases:
            path.write_text(raw)
            result = self.cli()
            self.assertEqual(result.returncode, 2)
            self.assertNotIn('PRIVATE_', result.stderr + result.stdout)
        path.write_text(original)
        (self.root / 'configuration.original.json').write_text('{}')
        self.assertEqual(self.cli().returncode, 2)

    def test_success_output_omits_source_prompts_argv_unknown_fields_and_raw_errors(self):
        self.report['attempts'][0].update(execution=execution(False), passed=False)
        self.report['passed'] = False
        self.write()
        for flags in ((), ('--json',)):
            result = self.cli(*flags)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertNotIn('PRIVATE_', result.stdout + result.stderr)
            self.assertIn('baseline', result.stdout)
        self.assertEqual(MODULE.summarize(self.root)['configuration_sha256'],
                         hashlib.sha256((self.root / 'configuration.original.json').read_bytes()).hexdigest())

    def test_extreme_finite_times_cannot_overflow_medians_or_underflow_ratios(self):
        self.assertEqual(MODULE.distribution([1e308, 1e308])['median'], 1e308)
        self.assertEqual(MODULE.distribution([0, 1e308])['median'], 5e307)
        self.report['attempts'][0]['execution']['elapsed_seconds'] = 1e308
        self.report['attempts'][1]['execution']['elapsed_seconds'] = 1e-308
        result = self.summarize()
        self.assertEqual(result['pairs'][0]['both_passed'], 1)
        self.assertEqual(result['pairs'][0]['right_over_left_elapsed_both_passed']['known'], 0)
        self.assertEqual(self.cli('--json').returncode, 0)

    def test_input_bound_and_symlink_refusal(self):
        self.write()
        path = self.root / 'comparison.json'
        path.unlink()
        path.symlink_to(self.root / 'configuration.original.json')
        self.assertEqual(self.cli().returncode, 2)
        path.unlink()
        with path.open('wb') as stream:
            stream.truncate(MODULE.MAX_DOCUMENT_BYTES + 1)
        self.assertEqual(self.cli().returncode, 2)

    def test_argument_errors_do_not_echo_values(self):
        self.write()
        for flags in (('--json=PRIVATE_SECRET',), ('--PRIVATE_SECRET',)):
            result = self.cli(*flags)
            self.assertEqual(result.returncode, 2)
            self.assertNotIn('PRIVATE_SECRET', result.stdout + result.stderr)

    def test_real_offline_runner_failures_are_summarized_as_fixture_evidence(self):
        output = self.root / 'controls'
        process = subprocess.run([sys.executable, str(ROOT / 'examples/evaluation/run-offline-controls.py'),
                                  '--output', str(output)], capture_output=True, text=True, timeout=45)
        self.assertEqual(process.returncode, 0, process.stdout + process.stderr)
        result = MODULE.summarize(output / 'evidence')
        self.assertEqual(result['mode'], 'fixture')
        self.assertIn('not an AI benchmark', result['claim'])
        arms = {arm['arm']: arm for arm in result['arms']}
        self.assertEqual(arms['correct']['passed'], 1)
        self.assertEqual(arms['incorrect']['failed_checks'], 1)
        self.assertEqual(arms['nonzero']['actor_failures'], 1)
        self.assertEqual(arms['timeout']['actor_timeouts'], 1)


if __name__ == '__main__':
    unittest.main()
