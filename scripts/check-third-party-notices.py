#!/usr/bin/env python3
"""Reproduce selected upstream notices for every released binary target.

Selection is explicit in third-party-notice-sources.json; this does not infer
licenses or legal classifications. Default/--check verifies bytes. --write is
required to replace the generated notice after reviewing dependency changes.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys

RELEASE_TARGETS = {'linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64'}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def command(go, root, arguments, environment):
    result = subprocess.run([go, *arguments], cwd=root, env=environment,
                            check=True, capture_output=True, text=True, timeout=120)
    return result.stdout


def objects(raw):
    decoder, offset = json.JSONDecoder(), 0
    while offset < len(raw):
        while offset < len(raw) and raw[offset].isspace():
            offset += 1
        if offset < len(raw):
            value, offset = decoder.raw_decode(raw, offset)
            yield value


def literal(directory, relative):
    path = Path(relative)
    require(not path.is_absolute() and '..' not in path.parts, 'notice paths must stay relative')
    source = directory / path
    require(source.is_file() and not source.is_symlink(), 'missing regular selected notice: ' + relative)
    data = source.read_bytes()
    require(0 < len(data) <= 2 << 20, 'selected notice must be bounded and nonempty: ' + relative)
    data.decode('utf-8')
    return data


def generate(root, go, manifest_path):
    selection = json.loads(manifest_path.read_text(encoding='utf-8'))
    require(selection.get('version') == 1, 'unsupported notice selection version')
    targets = selection.get('targets')
    require(isinstance(targets, list) and len(targets) == len(RELEASE_TARGETS)
            and all(isinstance(target, str) for target in targets)
            and set(targets) == RELEASE_TARGETS,
            'notice selection must cover all four distinct release targets')
    environment = dict(os.environ, CGO_ENABLED='0')
    version = command(go, root, ['env', 'GOVERSION'], environment).strip()
    require(version == selection['go_version'], 'use pinned release Go toolchain ' + selection['go_version'])
    go_root = Path(command(go, root, ['env', 'GOROOT'], environment).strip())
    modules = {}
    main_path = command(go, root, ['list', '-m', '-f', '{{.Path}}'], environment).strip()
    for target in targets:
        system, architecture = target.split('/')
        target_env = dict(environment, GOOS=system, GOARCH=architecture)
        raw = command(go, root, ['list', '-deps', '-json', './cmd/agentmux'], target_env)
        for package in objects(raw):
            module = package.get('Module')
            if not module or module['Path'] == main_path:
                continue
            require(not module.get('Replace'), 'review replacement module notices: ' + module['Path'])
            name = module['Path']
            require(name in selection['modules'], 'runtime dependency lacks selected notices: ' + name)
            if name in modules:
                require(modules[name]['Version'] == module['Version'], 'target module versions differ: ' + name)
            modules[name] = module
    require(modules, 'no third-party runtime modules were selected')
    sections = []
    for name, module in sorted(modules.items()):
        files = selection['modules'][name]
        require(isinstance(files, list) and files, 'runtime dependency has no selected notice files: ' + name)
        for relative in files:
            sections.append((name + ' ' + module['Version'] + ' — ' + relative,
                             literal(Path(module['Dir']), relative)))
    for relative in selection['go_files']:
        sections.append(('Go ' + version + ' — ' + relative, literal(go_root, relative)))
    for supplement in selection['supplements']:
        sections.append((supplement['title'] + '\nSource: ' + supplement['source'],
                         literal(manifest_path.parent, supplement['file'])))
    header = '''Agentmux — Third-party notices
=============================

Agentmux's own code is licensed under the MIT license in LICENSE. Third-party
components retain their respective copyrights and license terms reproduced here.

This notice covers the Go dependency modules selected for the four release
binary targets (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, CGO disabled),
the Go runtime/standard library and its vendored packages, and Unicode data.
Some component notices cover upstream portions not selected in every target.
External provider CLIs and system tools are installed separately and are not
bundled in the Agentmux release archives.

Component inventory
-------------------
'''
    output = bytearray(header.encode('utf-8'))
    for name, module in sorted(modules.items()):
        output.extend(('- ' + name + ' ' + module['Version'] + '\n').encode('utf-8'))
    output.extend(('- Go ' + version + ' runtime/standard library and selected vendor notices\n- Unicode data\n').encode('utf-8'))
    for title, data in sections:
        output.extend(('\n' + '=' * 78 + '\n' + title + '\n' + '=' * 78 + '\n\n').encode('utf-8'))
        output.extend(data)
        if not data.endswith(b'\n'):
            output.extend(b'\n')
    return bytes(output), len(modules), len(sections)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument('--go', default='go')
    parser.add_argument('--sources', type=Path, default=Path(__file__).with_name('third-party-notice-sources.json'))
    parser.add_argument('--output', type=Path)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--check', action='store_true')
    mode.add_argument('--write', action='store_true')
    args = parser.parse_args(argv)
    root = args.root.resolve()
    output = args.output or root / 'docs/third-party-notices.txt'
    try:
        expected, modules, sections = generate(root, args.go, args.sources.resolve())
        if args.write:
            output.write_bytes(expected)
        else:
            require(output.is_file() and output.read_bytes() == expected,
                    'third-party notices differ; review upstream selection, then run with --write')
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        parser.exit(1, 'Third-party notice check failed: ' + str(error) + '\n')
    print(('Wrote' if args.write else 'Verified') + f' third-party notices: {modules} runtime modules, {sections} sections')
    return 0


if __name__ == '__main__':
    sys.exit(main())
