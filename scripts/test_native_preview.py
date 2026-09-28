"""Exercise the native boundary using synthetic fixtures, no private originals."""
import concurrent.futures
import os
import subprocess
import sys

helper = sys.argv[1]
width, height = 321, 217
ppm = f'P6\n{width} {height}\n255\n'.encode() + bytes([80, 140, 220]) * width * height

def jpeg(*args):
    return subprocess.run(['cjpeg', *args], input=ppm, capture_output=True, check=True).stdout

def run(data, size='320', valid=True):
    proc = subprocess.run([helper, size], input=data, capture_output=True, timeout=40)
    if b'Sanitizer' in proc.stderr or b'runtime error:' in proc.stderr:
        raise AssertionError(proc.stderr.decode(errors='replace'))
    if valid:
        assert proc.returncode == 0, proc.stderr
        decoded = subprocess.run(['djpeg'], input=proc.stdout, capture_output=True, check=True).stdout
        assert decoded.startswith(b'P6\n320 216\n255\n'), decoded[:40]
    else:
        assert proc.returncode != 0, 'invalid input accepted'

baseline = jpeg()
for fixture in [baseline, jpeg('-progressive'), jpeg('-grayscale')]:
    run(fixture)
for fixture in [b'', b'not an image', baseline[:len(baseline)//2], baseline[:-2]]:
    run(fixture, valid=False)
# Corrupt the baseline SOF dimensions before any raster allocation.
large = bytearray(baseline)
sof = large.index(b'\xff\xc0')
large[sof+5:sof+9] = (20000).to_bytes(2, 'big') * 2
run(large, valid=False)
for size in ['0', '95', '1281', '-1', '320oops']:
    run(baseline, size=size, valid=False)
with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
    list(pool.map(run, [baseline]*16))
print('native progressive/grayscale/corruption/limits/parallel checks passed')
