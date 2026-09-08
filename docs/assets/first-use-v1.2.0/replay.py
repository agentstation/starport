#!/usr/bin/env python3
"""Render captured terminal events without repeating installation or inference."""
import argparse
import json
from pathlib import Path
import sys
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('capture', type=Path)
    parser.add_argument('--original', action='store_true')
    args = parser.parse_args()
    report = json.loads(args.capture.read_text())
    if report.get('verdict') != 'PASS':
        raise SystemExit('A successful capture is required.')
    events = report['events']
    previous = 0
    schedule = 0
    started = time.monotonic()
    for index, event in enumerate(events):
        gap = event['seconds'] - previous
        if not args.original:
            # Index 8 starts provider setup after the hidden credential prompt.
            if index == 8:
                gap = 8
            # Keep the installed version visible for six seconds.
            if index == 5:
                gap = 6
        schedule += gap
        time.sleep(max(0, started + schedule - time.monotonic()))
        data = event['data']
        if not args.original and data.startswith('\033[2J\033[H'):
            data = data.replace('  /  first request\n\n', '  /  first request\n'
                '\033[90mv1.2.0 · setup wait shortened · inference at original speed\033[0m\n\n')
        sys.stdout.write(data)
        sys.stdout.flush()
        previous = event['seconds']
    time.sleep(5 if args.original else 8)
    print('\n', flush=True)


if __name__ == '__main__':
    main()
