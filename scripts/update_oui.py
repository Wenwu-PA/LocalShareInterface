"""Refresh the offline IEEE MA-L vendor table; no download during app startup."""
from pathlib import Path
import csv
import gzip
import io
import json
import urllib.request

URL = 'https://standards-oui.ieee.org/oui/oui.csv'
ROOT = Path(__file__).resolve().parents[1]
with urllib.request.urlopen(URL, timeout=60) as response:
    raw = response.read(20_000_000).decode('utf-8-sig')
rows = csv.DictReader(io.StringIO(raw))
vendors = {r['Assignment'].upper(): r['Organization Name'] for r in rows if r['Registry'] == 'MA-L' and len(r['Assignment']) == 6}
assert len(vendors) > 10000, 'Unexpected IEEE response'
target = ROOT / 'internal/network/oui.json.gz'
target.write_bytes(gzip.compress(json.dumps(vendors, ensure_ascii=False, separators=(',', ':')).encode('utf-8'), mtime=0))
print(f'{len(vendors)} prefixes, {target.stat().st_size} compressed bytes')
