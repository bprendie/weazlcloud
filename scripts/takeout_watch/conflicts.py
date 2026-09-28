"""Verify deterministic conflict destinations without losing either version."""
import hashlib
import posixpath
import urllib.parse


def entry_key(entry):
    return entry.filename, entry.CRC, entry.file_size


def conflict_destination(target, entry, variant=0):
    identity = f'{target}\0{entry.filename}\0{entry.CRC}\0{entry.file_size}'
    suffix = hashlib.sha256(identity.encode()).hexdigest()[:32]
    if variant > 0:
        suffix += f'-{variant+1}'
    # Match Go path.Ext, including a leading-dot filename.
    base = posixpath.basename(target)
    ext = base[base.rfind('.'):] if '.' in base else ''
    stem = target[:-len(ext)] if ext else target
    return f'{stem} (takeout-{suffix}){ext}'


def folder_destination(name, variant):
    suffix = hashlib.sha256(name.encode()).hexdigest()[:32]
    if variant > 0:
        suffix += f'-{variant+1}'
    return f'{name} (takeout-folder-{suffix})'


def routed_destination(target, directories):
    for original in sorted(directories, key=len, reverse=True):
        if target.startswith(original+'/'):
            return directories[original] + target[len(original):]
    return target


def verify_directories(z, summary, destination):
    ancestors = set()
    for entry in z.infolist():
        target = destination(entry.filename)
        if entry.is_dir():
            ancestors.add(target)
        while '/' in target:
            target = target.rsplit('/', 1)[0]
            ancestors.add(target)
    directories = {}
    for record in sorted(summary.get('directories', []), key=lambda r: len(r['original'])):
        original, variant = record['original'], record.get('variant', 0)
        if original not in ancestors or not isinstance(variant, int) or variant < 0:
            raise RuntimeError('invalid directory conflict mapping')
        expected = folder_destination(routed_destination(original, directories), variant)
        if record['path'] != expected:
            raise RuntimeError('invalid directory conflict destination')
        directories[original] = expected
    return directories


def verify_renames(z, summary, api, destination, digest, directories):
    entries = {entry_key(f): f for f in z.infolist() if not f.is_dir()}
    mappings = {}
    for record in summary.get('renamed', []):
        key = record['source'], record['crc32'], record['size']
        entry = entries.get(key)
        if entry is None:
            raise RuntimeError('conflict mapping has no source ZIP member')
        original = routed_destination(destination(entry.filename), directories)
        variant = record.get('variant', 0)
        if not isinstance(variant, int) or variant < 0:
            raise RuntimeError('invalid conflict variant')
        target = conflict_destination(original, entry, variant)
        if record['original'] != original or record['path'] != target:
            raise RuntimeError('invalid conflict destination mapping')
        with z.open(entry) as reader:
            expected, size = digest(reader)
        if expected != record['sha256'] or size != entry.file_size:
            raise RuntimeError('conflict source hash mismatch')
        with api.open('/api/library?path='+urllib.parse.quote(target, safe='')) as reader:
            actual, stored_size = digest(reader)
        if actual != expected or stored_size != size:
            raise RuntimeError('conflict stored file hash mismatch')
        mappings[key] = record
    return mappings
