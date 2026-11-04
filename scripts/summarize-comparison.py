#!/usr/bin/env python3
"""Describe retained matched attempts without inferring a winner or model quality.

Read a comparison evidence directory; never invoke actors, graders or Git.
Declared arm IDs appear in the output. Source paths, prompts, argv, logs and raw
errors do not. This is a consistency-checked summary, not an attestation.
"""
import argparse
import hashlib
import itertools
import json
import math
import os
from pathlib import Path
import re
import stat
import sys


MAX_DOCUMENT_BYTES = 16 << 20
MAX_ATTEMPTS = 200


class InvalidEvidence(ValueError):
    pass


class SafeParser(argparse.ArgumentParser):
    def error(self, _message):
        self.exit(2, 'comparison summary refused: invalid arguments; use --help\n')


def require(condition):
    if not condition:
        raise InvalidEvidence('inconsistent or unsupported comparison evidence')


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result)
        result[key] = value
    return result


def read_document(path):
    require(not path.is_symlink())
    descriptor = os.open(path, os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0) | getattr(os, 'O_NONBLOCK', 0))
    with os.fdopen(descriptor, 'rb') as stream:
        metadata = os.fstat(stream.fileno())
        require(stat.S_ISREG(metadata.st_mode) and metadata.st_size <= MAX_DOCUMENT_BYTES)
        data = stream.read(MAX_DOCUMENT_BYTES + 1)
    require(len(data) <= MAX_DOCUMENT_BYTES)
    def invalid_constant(_):
        raise InvalidEvidence('non-finite JSON number')
    value = json.loads(data, object_pairs_hook=unique_object, parse_constant=invalid_constant)
    require(type(value) is dict)
    return value, hashlib.sha256(data).hexdigest()


def number(value):
    return type(value) in (int, float) and math.isfinite(value) and value >= 0


def identifier(value):
    return type(value) is str and re.fullmatch(r'[a-z][a-z0-9-]{0,47}', value) is not None


def digest(value):
    return type(value) is str and re.fullmatch(r'[0-9a-f]{64}', value) is not None


def entries(value, maximum):
    require(type(value) is list and 1 <= len(value) <= maximum)
    require(all(type(item) is dict and identifier(item.get('id')) for item in value))
    require(len({item['id'] for item in value}) == len(value))
    return value


def expected_order(tasks, arms, repeats):
    for repeat in range(repeats):
        for task_number, task in enumerate(tasks):
            shift = (repeat + task_number) % len(arms)
            order = arms[shift:] + arms[:shift]
            if (repeat // len(arms)) % 2:
                order = list(reversed(order))
            for position, arm in enumerate(order):
                yield task['id'], arm['id'], repeat + 1, position + 1


def validate_command(result, *, check=False):
    require(type(result) is dict and type(result.get('passed')) is bool)
    require(type(result.get('timed_out')) is bool)
    require(result.get('exit_code') is None or type(result['exit_code']) is int)
    require(result.get('error') is None or type(result['error']) is str)
    cleanup = result.get('cleanup_errors')
    require(type(cleanup) is list and len(cleanup) <= 100 and all(type(error) is str for error in cleanup))
    require(result.get('elapsed_seconds') is None or number(result['elapsed_seconds']))
    successful = (result.get('exit_code') == 0 and not result['timed_out']
                  and result.get('error') is None and not cleanup)
    if check:
        require(type(result.get('artifact_intact')) is bool and type(result.get('evaluator_intact')) is bool)
        intact = (digest(result.get('artifact_before_sha256'))
                  and result.get('artifact_before_sha256') == result.get('artifact_after_sha256'))
        require(not result['artifact_intact'] or intact)
        successful = successful and result['artifact_intact'] and result['evaluator_intact']
    require(result['passed'] is successful)


def distribution(values):
    ordered = sorted(values)
    middle = len(ordered) // 2
    median = None
    if ordered:
        median = ordered[middle]
        if len(ordered) % 2 == 0:
            lower = ordered[middle - 1]
            median = lower + (median - lower) / 2
    return {'known': len(values), 'min': min(values) if values else None,
            'median': median,
            'max': max(values) if values else None}


def summarize(directory):
    root = Path(directory)
    original, original_hash = read_document(root / 'configuration.original.json')
    report, report_hash = read_document(root / 'comparison.json')
    require(type(original.get('version')) is int and original['version'] == 1)
    require(type(report.get('version')) is int and report['version'] == 1)
    mode = original.get('mode')
    require(mode in ('fixture', 'live') and report.get('mode') == mode)
    require(report.get('configuration_sha256') == original_hash)
    source, recorded_source = original.get('source'), report.get('source')
    require(type(source) is dict and type(recorded_source) is dict)
    commit = source.get('commit')
    require(type(commit) is str and re.fullmatch(r'[0-9a-f]{40}|[0-9a-f]{64}', commit) is not None
            and recorded_source.get('commit') == commit and digest(report.get('snapshot_sha256')))
    tasks = entries(original.get('tasks'), 20)
    arms = entries(original.get('arms'), 8)
    require(len(arms) >= 2)
    repeats = original.get('repeats', 2)
    require(type(repeats) is int and 1 <= repeats <= 20)
    checks = {task['id']: entries(task.get('checks'), 20) for task in tasks}
    planned = len(tasks) * len(arms) * repeats
    require(planned <= MAX_ATTEMPTS and type(report.get('planned_attempts')) is int
            and report['planned_attempts'] == planned)
    require(type(report.get('completed')) is bool and type(report.get('passed')) is bool)
    require(report.get('error') is None or type(report['error']) is str)
    attempts = report.get('attempts')
    require(type(attempts) is list and len(attempts) <= planned)
    grouped = {arm['id']: [] for arm in arms}
    indexed = {}
    for attempt, expected in zip(attempts, expected_order(tasks, arms, repeats)):
        require(type(attempt) is dict)
        require(type(attempt.get('repeat')) is int and type(attempt.get('position')) is int)
        actual = (attempt.get('task'), attempt.get('arm'), attempt['repeat'], attempt['position'])
        require(actual == expected)
        require(type(attempt.get('completed')) is bool and type(attempt.get('passed')) is bool)
        execution = attempt.get('execution')
        if execution is not None:
            validate_command(execution)
        results = attempt.get('checks')
        require(type(results) is list and len(results) <= len(checks[attempt['task']]))
        for result, definition in zip(results, checks[attempt['task']]):
            require(type(result) is dict and result.get('id') == definition['id'])
            validate_command(result, check=True)
        if attempt['completed']:
            require(execution is not None and len(results) == len(checks[attempt['task']]))
        passed = (attempt['completed'] and execution is not None and execution['passed']
                  and len(results) == len(checks[attempt['task']]) and all(item['passed'] for item in results))
        require(attempt['passed'] is passed)
        grouped[attempt['arm']].append(attempt)
        indexed[attempt['task'], attempt['repeat'], attempt['arm']] = attempt
    if report['completed']:
        require(len(attempts) == planned and all(item['completed'] for item in attempts)
                and report.get('error') is None)
    require(report['passed'] is (report['completed'] and all(item['passed'] for item in attempts)))
    arm_summaries = []
    for arm in arms:
        selected = grouped[arm['id']]
        executions = [item['execution'] for item in selected if item.get('execution') is not None]
        grades = [grade for item in selected for grade in item['checks']]
        elapsed = [item['elapsed_seconds'] for item in executions if number(item.get('elapsed_seconds'))]
        successful_elapsed = [item['execution']['elapsed_seconds'] for item in selected
                              if item['passed'] and number(item['execution'].get('elapsed_seconds'))]
        arm_summaries.append({
            'arm': arm['id'], 'planned': len(tasks) * repeats, 'attempted': len(selected),
            'not_started': len(tasks) * repeats - len(selected),
            'completed': sum(item['completed'] for item in selected),
            'passed': sum(item['passed'] for item in selected),
            'failed': sum(item['completed'] and not item['passed'] for item in selected),
            'incomplete': sum(not item['completed'] for item in selected),
            'actor_failures': sum(not item['passed'] for item in executions),
            'actor_timeouts': sum(item['timed_out'] for item in executions),
            'failed_checks': sum(not item['passed'] for item in grades),
            'check_timeouts': sum(item['timed_out'] for item in grades),
            'cleanup_failures': sum(bool(item['cleanup_errors']) for item in executions + grades),
            'actor_elapsed_seconds_all': distribution(elapsed),
            'actor_elapsed_seconds_passed': distribution(successful_elapsed),
        })
    pairs = []
    for left, right in itertools.combinations([arm['id'] for arm in arms], 2):
        pair = {'left': left, 'right': right, 'planned_pairs': len(tasks) * repeats,
                'both_passed': 0, 'only_left_passed': 0, 'only_right_passed': 0,
                'neither_passed': 0, 'incomplete_pairs': 0}
        ratios = []
        for task in tasks:
            for repeat in range(1, repeats + 1):
                a, b = (indexed.get((task['id'], repeat, arm)) for arm in (left, right))
                if a is None or b is None or not a['completed'] or not b['completed']:
                    pair['incomplete_pairs'] += 1
                elif a['passed'] and b['passed']:
                    pair['both_passed'] += 1
                    durations = [item['execution'].get('elapsed_seconds') for item in (a, b)]
                    if all(number(value) and value > 0 for value in durations):
                        ratio = durations[1] / durations[0]
                        if math.isfinite(ratio) and ratio > 0:
                            ratios.append(ratio)
                elif a['passed']:
                    pair['only_left_passed'] += 1
                elif b['passed']:
                    pair['only_right_passed'] += 1
                else:
                    pair['neither_passed'] += 1
        pair['right_over_left_elapsed_both_passed'] = distribution(ratios)
        pairs.append(pair)
    return {
        'schema_version': 1, 'mode': mode,
        'claim': 'offline harness controls; not an AI benchmark' if mode == 'fixture'
                 else 'descriptive matched-attempt evidence; no superiority inferred',
        'input_sha256': report_hash, 'configuration_sha256': original_hash,
        'completed': report['completed'], 'passed': report['passed'],
        'planned_attempts': planned, 'attempted': len(attempts),
        'distinct_tasks': len(tasks), 'repeats': repeats,
        'planned_order_balanced': repeats % (2 * len(arms)) == 0,
        'arms': arm_summaries, 'pairs': pairs,
        'human_interventions': None, 'inference_requests': None, 'tokens': None, 'cost': None,
        'limitations': [
            'Failures, incomplete attempts and unstarted attempts remain separate.',
            'Paired timing ratios include only pairs where both artifacts passed; inspect all outcome counts.',
            'Repeated runs of the same task are not independent task samples; no confidence interval or winner is inferred.',
            'Actor elapsed time includes command execution and cleanup, not source preparation or external grading.',
            'This summary checks recorded consistency, not the authenticity of logs, graders or candidate bytes.',
        ],
    }


def human(result):
    lines = [result['claim'], f"Tasks: {result['distinct_tasks']}; repeats: {result['repeats']}; attempted: {result['attempted']}/{result['planned_attempts']}",
             'Arm | passed | failed | incomplete | not started | actor timeouts | cleanup failures']
    for arm in result['arms']:
        lines.append(' | '.join(str(arm[key]) for key in ('arm', 'passed', 'failed', 'incomplete', 'not_started', 'actor_timeouts', 'cleanup_failures')))
    for pair in result['pairs']:
        timing = pair['right_over_left_elapsed_both_passed']
        lines.append(f"{pair['left']} / {pair['right']}: both passed {pair['both_passed']}; only left {pair['only_left_passed']}; only right {pair['only_right_passed']}; neither {pair['neither_passed']}; incomplete {pair['incomplete_pairs']}; timing pairs {timing['known']}; median right/left {timing['median']}")
    lines.append('Human intervention, inference requests, tokens and cost: unknown.')
    lines.extend(result['limitations'])
    return '\n'.join(lines) + '\n'


def main(argv=None):
    parser = SafeParser(prog='summarize-comparison.py', description=__doc__)
    parser.add_argument('evidence', help='comparison evidence directory; input files are limited to 16 MiB each')
    parser.add_argument('--json', action='store_true')
    args = parser.parse_args(argv)
    try:
        result = summarize(args.evidence)
        rendered = json.dumps(result, indent=2, allow_nan=False) + '\n' if args.json else human(result)
    except (OSError, ValueError, TypeError, KeyError, RecursionError, OverflowError):
        print('comparison summary refused: invalid, inconsistent or unreadable evidence', file=sys.stderr)
        return 2
    sys.stdout.write(rendered)
    return 0


if __name__ == '__main__':
    sys.exit(main())
