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

# Two outputs share one native decode and carry explicit bounded frames.
import struct
for fixture in [baseline,jpeg('-progressive'),jpeg('-grayscale')]:
    result=subprocess.run([helper,'--bundle','320,1280'],input=fixture,capture_output=True,check=True,timeout=40)
    assert b'Sanitizer' not in result.stderr and b'runtime error:' not in result.stderr
    data=result.stdout;seen=[]
    while data:
        assert len(data)>=7
        size,kind,length=struct.unpack('>HBI',data[:7]);data=data[7:]
        assert length<=8*1024*1024 and length<=len(data)
        body,data=data[:length],data[length:]
        if kind==0:
            assert not size and not length and not data
            break
        assert kind==1
        decoded=subprocess.run(['djpeg'],input=body,capture_output=True,check=True).stdout
        assert decoded.startswith(f'P6\n{size} '.encode()),decoded[:40]
        seen.append(size)
    assert seen==[320,1280],seen
for sizes in ['320,320','95,320','320,1281','320oops,1280']:
    result=subprocess.run([helper,'--bundle',sizes],input=baseline,capture_output=True,timeout=40)
    assert result.returncode!=0
print('native shared-decode bundle framing/limits passed')

# Full 100-MP camera JPEGs: scaled decode must work for baseline and progressive.
# Keep the source grayscale to avoid a large Python RGB allocation.
large_pgm = b'P5\n11656 8742\n255\n' + b'\x80' * (11656 * 8742)
for flags in [[], ['-progressive']]:
    source = subprocess.run(['cjpeg', '-grayscale', *flags], input=large_pgm,
                            capture_output=True, check=True).stdout
    proc = subprocess.run([helper, '320'], input=source, capture_output=True, timeout=40)
    assert proc.returncode == 0, proc.stderr
    assert b'Sanitizer' not in proc.stderr and b'runtime error:' not in proc.stderr
    decoded = subprocess.run(['djpeg'], input=proc.stdout, capture_output=True, check=True).stdout
    assert decoded.startswith(b'P6\n320 240\n255\n'), decoded[:40]
print('native 100-MP baseline/progressive JPEG checks passed')
