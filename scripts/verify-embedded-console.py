#!/usr/bin/env python3
"""Check that a release binary contains the complete console build."""

import argparse
import mmap
from pathlib import Path


def verify(binary, directory):
    files = sorted(path for path in directory.rglob('*') if path.is_file() and path.name != '.gitkeep')
    if not (directory / 'index.html').is_file() or not any(path.suffix == '.js' for path in files) or not any(path.suffix == '.css' for path in files):
        raise ValueError('The console build must contain HTML, JavaScript, and CSS.')
    with binary.open('rb') as source, mmap.mmap(source.fileno(), 0, access=mmap.ACCESS_READ) as contents:
        for path in files:
            data = path.read_bytes()
            if not data or contents.find(data) < 0:
                raise ValueError(f'The release binary lacks console asset {path.relative_to(directory)}.')
    return len(files)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=Path)
    parser.add_argument('directory', type=Path)
    args = parser.parse_args()
    count = verify(args.binary, args.directory)
    print(f'PASS {count} console files embedded in {args.binary.name}')
