#!/usr/bin/env python3
"""Atomically install a locally built Agentmux and its amx shortcut."""
import argparse
import importlib.util
from pathlib import Path
import sys

spec=importlib.util.spec_from_file_location('agentmux_installer',Path(__file__).with_name('install-release.py'))
installer=importlib.util.module_from_spec(spec);spec.loader.exec_module(installer)

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source',required=True)
    parser.add_argument('--destination',required=True)
    parser.add_argument('--replace',action='store_true')
    args=parser.parse_args()
    binary=installer.read_bounded(args.source,128*1024*1024)
    print(installer.install_binary(binary,args.destination,args.replace))

if __name__=='__main__':
    try:main()
    except (ValueError,OSError) as error:
        print('error: '+str(error),file=sys.stderr)
        sys.exit(1)
