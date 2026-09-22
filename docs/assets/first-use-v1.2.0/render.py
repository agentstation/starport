#!/usr/bin/env python3
"""Render captured output with explicit edits outside the inference interval."""
import argparse
import hashlib
import json
from pathlib import Path
import re

from PIL import Image, ImageDraw, ImageFont

WIDTH, HEIGHT = 1280, 800
FONT_SIZE = 26
FONT = '/System/Library/Fonts/Menlo.ttc'
ANSI = re.compile(r'\x1b\[[0-9;]*[A-Za-z]')


def render_screen(text, edited):
    image = Image.new('RGB', (WIDTH, HEIGHT), '#0b1019')
    draw = ImageDraw.Draw(image)
    draw.rounded_rectangle((16, 16, WIDTH - 16, HEIGHT - 16), radius=18, outline='#283447', width=2)
    font = ImageFont.truetype(FONT, FONT_SIZE)
    title = ImageFont.truetype(FONT, 30, index=1)
    footer = ImageFont.truetype(FONT, 20)
    lines = ANSI.sub('', text).splitlines()
    for index, line in enumerate(lines):
        selected = title if index == 0 else font
        color = '#76dec3' if index == 0 else '#a5b8dc' if index == 2 else '#e6ecf5'
        if line.startswith('$'):
            color = '#a5b8dc'
        if draw.textlength(line, font=selected) > WIDTH - 112:
            raise ValueError('A captured line exceeds the readable frame width: ' + line)
        y = 46 + index * 32
        if y + 32 > HEIGHT - 74:
            raise ValueError('The capture exceeds the readable frame height.')
        draw.text((56, y), line, font=selected, fill=color)
    label = ('Setup wait shortened. Reading pauses added. Inference timing unchanged.' if edited
             else 'Original capture timing. Credentials hidden. Startup logs omitted.')
    draw.text((56, HEIGHT - 52), label, font=footer, fill='#a5b8dc')
    return image


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('capture', type=Path)
    parser.add_argument('output', type=Path)
    args = parser.parse_args()
    report = json.loads(args.capture.read_text())
    if report.get('verdict') != 'PASS':
        raise SystemExit('A successful capture is required.')
    args.output.mkdir(parents=True, exist_ok=True)
    manifest = {'capture_sha256': hashlib.sha256(args.capture.read_bytes()).hexdigest(),
                'width': WIDTH, 'height': HEIGHT, 'font_size': FONT_SIZE,
                'font': FONT, 'effective_font_at_900px': FONT_SIZE * 900 / WIDTH,
                'edits': [], 'outputs': {}}
    for edited in [False, True]:
        frames, durations, times = [], [], []
        state, elapsed, previous = '', 0, 0
        for index, event in enumerate(report['events']):
            gap = event['seconds'] - previous
            if edited and index in (5, 8):
                replacement = 6 if index == 5 else 8
                if previous >= report['inference_start_seconds']:
                    raise ValueError('An edit would change the inference interval.')
                manifest['edits'].append({'before_event': index, 'original_gap_seconds': gap,
                                          'edited_gap_seconds': replacement})
                gap = replacement
            elapsed += gap
            data = event['data']
            if data.startswith('\033[2J\033[H'):
                state = data
            else:
                state += data
            image = render_screen(state, edited)
            # GIF timestamps use centiseconds. Round cumulative time to prevent drift.
            timestamp = round(elapsed * 100) * 10
            if times and timestamp == times[-1]:
                frames[-1] = image
            else:
                frames.append(image)
                times.append(timestamp)
            previous = event['seconds']
        durations = [b - a for a, b in zip(times, times[1:])] + [8000 if edited else 5000]
        label = 'first-use' if edited else 'first-use-uncut'
        path = args.output / (label + '.gif')
        frames[0].save(path, save_all=True, append_images=frames[1:], duration=durations, loop=0, optimize=True, disposal=2)
        manifest['outputs'][path.name] = {'sha256': hashlib.sha256(path.read_bytes()).hexdigest(),
                                         'bytes': path.stat().st_size, 'duration_ms': sum(durations),
                                         'event_frames': len(frames)}
        if edited:
            for index, frame in enumerate(frames):
                frame.save(args.output / ('frame-%02d.png' % index))
            manifest['edited_event_times_ms'] = times
    (args.output / 'render.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print(json.dumps(manifest, indent=2))


if __name__ == '__main__':
    main()
