import io
import tempfile
import unittest
import zipfile
from pathlib import Path
from unittest.mock import patch
from support import destination, inventory, signature, validate, verify
from watch import Watch


class WatchTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root=Path(self.temp.name)
        self.stage=root/'stage'; self.stage.mkdir()
        work=root/'work'; work.mkdir()
        self.cfg={'work':str(work),'stage':str(self.stage),'prefix':'takeout-test-','minimum_files':3,'last_name':'takeout-test-2-001.zip'}
        self.w=Watch(self.cfg)

    def archive(self,name,entries=None):
        p=self.stage/name
        with zipfile.ZipFile(p,'w',compression=zipfile.ZIP_STORED) as z:
            for name,data in (entries or {'Takeout/Drive/a.txt':b'hello'}).items(): z.writestr(name,data)
        return p

    @patch('watch.open_writers',return_value=[])
    def test_waits_for_sentinel_count_stability_and_gaps(self,_):
        self.archive('takeout-test-1-001.zip')
        self.archive('takeout-test-1-002.zip')
        self.assertFalse(self.w.waiting())
        self.archive(self.cfg['last_name'])
        self.assertFalse(self.w.waiting())
        self.w.state['stable_since']-=1200
        with patch('watch.open_writers',return_value=[self.cfg['last_name']]): self.assertFalse(self.w.waiting())
        self.assertTrue(self.w.waiting())
        self.assertEqual(self.w.state['phase'],'importing')
        self.assertEqual(len(self.w.state['archives']),3)

    @patch('watch.open_writers',return_value=[])
    def test_missing_part_never_starts(self,_):
        for name in ['takeout-test-1-001.zip','takeout-test-1-003.zip',self.cfg['last_name']]: self.archive(name)
        self.w.waiting(); self.w.state['stable_since']-=1200
        self.assertFalse(self.w.waiting())

    def test_source_cleanup_requires_verified_unchanged_manifest(self):
        p=self.archive('takeout-test-1-001.zip')
        item={'signature':signature(p),'status':'running'}
        self.w.state['archives'][p.name]=item
        with self.assertRaises(RuntimeError): self.w.remove_verified(p.name,item)
        item.update(status='verified',verification={'files':1})
        with p.open('ab') as f: f.write(b'changed')
        with self.assertRaises(RuntimeError): self.w.remove_verified(p.name,item)
        self.assertTrue(p.exists())
        item['signature']=signature(p)
        with patch('watch.open_writers',return_value=[]): self.w.remove_verified(p.name,item)
        self.assertFalse(p.exists())
        self.assertEqual(item['status'],'removed')

    def test_crc_corruption_is_logged_good_entries_still_sampled(self):
        p=self.archive('takeout-test-1-001.zip',{'Takeout/Drive/good.txt':b'hello','Takeout/Drive/bad.txt':b'unique-payload'})
        p.write_bytes(p.read_bytes().replace(b'unique-payload',b'BROKEN-payload',1))
        result=validate(p,lambda *a,**k:None)
        self.assertEqual([e['path'] for e in result['errors']],['Takeout/Drive/bad.txt'])
        self.assertEqual(result['samples'][0]['path'],'Google Takeout/Drive/good.txt')
        summary={'files':2,'imported':1,'skipped':0,'corrupt':1,'bytes':19,'processed_bytes':19,'errors':result['errors']}
        class FakeAPI:
            def json(self,path): return {'files':[{'path':'Google Takeout/Drive/good.txt','size':5}]}
            def open(self,path): return io.BytesIO(b'hello')
        self.assertEqual(verify(p,result,summary,FakeAPI())['verified_files'],1)
        result['samples'][0]['sha256']='wrong'
        with self.assertRaises(RuntimeError): verify(p,result,summary,FakeAPI())

    def test_product_routing_and_unsafe_paths(self):
        self.assertEqual(destination('Takeout/Google Photos/Trip/a.jpg'),'Google Takeout/Photos/Trip/a.jpg')
        self.assertEqual(destination('Takeout/Drive/a.txt'),'Google Takeout/Drive/a.txt')
        for name in ['../escape','/absolute','Takeout/Drive/C:/bad']:
            with self.assertRaises(ValueError): destination(name)


if __name__=='__main__': unittest.main()
