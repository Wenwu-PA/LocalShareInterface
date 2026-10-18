"""Read an ongoing or completed soak without touching the running server."""
import argparse
import json
from pathlib import Path
from statistics import median

METRICS = ('rss', 'private', 'heap_bytes', 'goroutines', 'database_bytes', 'wal_bytes')


def summarize(folder):
    result = json.loads((folder / 'result.json').read_text(encoding='utf-8'))
    # The writer may currently be appending its last line. Never parse a partial
    # record; malformed complete records must still fail visibly.
    raw = (folder / 'samples.jsonl').read_bytes()
    lines = raw.split(b'\n')[:-1]
    samples = [json.loads(line) for line in lines if line.strip()]
    if not samples:
        raise ValueError('No complete samples yet')
    if any(b['seconds'] <= a['seconds'] for a, b in zip(samples, samples[1:])):
        raise ValueError('Sample timestamps are not strictly increasing')
    last = samples[-1]
    gaps = [b['seconds'] - a['seconds'] for a, b in zip(samples, samples[1:])]
    hours = []
    for hour in sorted({int(sample['seconds'] // 3600) for sample in samples}):
        group = [sample for sample in samples if int(sample['seconds'] // 3600) == hour]
        hours.append({'hour': hour, 'samples': len(group),
                      'first_second': group[0]['seconds'], 'last_second': group[-1]['seconds'],
                      'median': {key: median(sample[key] for sample in group) for key in METRICS},
                      'max': {key: max(sample[key] for sample in group) for key in METRICS}})
    full_day = (result.get('result') == 'PASS' and result.get('full_day') is True
                and result.get('seconds', 0) >= 86400 and last['seconds'] >= 86400)
    return {'status': result['result'], 'full_day_pass': full_day,
            'observed_seconds': last['seconds'], 'samples': len(samples),
            'max_sample_gap_seconds': round(max(gaps, default=0), 2),
            'cycles': last['cycles'], 'bytes_each_direction': last['cycles'] * result['file_bytes'],
            'sampled_max': {key: max(sample[key] for sample in samples) for key in METRICS},
            'latest': {key: last[key] for key in METRICS}, 'hours': hours,
            'interpretation': 'Observed samples only; no automatic claim of leak freedom or idle memory budget.'}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('folder', type=Path, help='Directory containing result.json and samples.jsonl')
    args = parser.parse_args()
    print(json.dumps(summarize(args.folder), indent=2))


if __name__ == '__main__':
    main()
