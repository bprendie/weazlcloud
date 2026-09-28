import hashlib
import io
import tempfile
import unittest
import urllib.parse
import zipfile
from pathlib import Path
from conflicts import conflict_destination, folder_destination
from support import validate, verify


class ConflictVerificationTests(unittest.TestCase):
    def test_aliases_and_parent_collisions_verify_content_and_reject_bad_mapping(self):
        with tempfile.TemporaryDirectory() as root:
            source = Path(root)/'batch.zip'
            entries = {'Takeout/Drive/name.txt':b'first', 'Takeout/Drive/ name.txt':b'second', 'Takeout/Drive/tree':b'file', 'Takeout/Drive/tree/child':b'child'}
            with zipfile.ZipFile(source,'w') as z:
                for name, body in entries.items(): z.writestr(name,body)
            prepared = validate(source, lambda *a, **k: None)
            with zipfile.ZipFile(source) as z:
                entry = z.infolist()[1]
                alternate = conflict_destination('name.txt',entry)
                record = {'source':entry.filename,'crc32':entry.CRC,'size':entry.file_size,'original':'name.txt','path':alternate,'sha256':hashlib.sha256(b'second').hexdigest()}
            folder = folder_destination('tree',0)
            stored = {'name.txt':b'first',alternate:b'second','tree':b'file',folder+'/child':b'child'}
            summary = {'files':4,'imported':4,'skipped':0,'bytes':20,'processed_bytes':20,'renamed':[record],'directories':[{'original':'tree','path':folder}]}
            class API:
                def json(self,path): return {'files':[{'path':p,'size':len(b)} for p,b in stored.items()]}
                def open(self,path): return io.BytesIO(stored[urllib.parse.parse_qs(urllib.parse.urlsplit(path).query)['path'][0]])
            self.assertEqual(verify(source,prepared,summary,API())['verified_files'],4)
            stored[alternate]=b'BROKEN'
            with self.assertRaisesRegex(RuntimeError,'stored file hash mismatch'):
                verify(source,prepared,summary,API())
            stored[alternate]=b'second'
            record['path']='unrelated.txt'
            with self.assertRaisesRegex(RuntimeError,'destination mapping'):
                verify(source,prepared,summary,API())


if __name__ == '__main__': unittest.main()
