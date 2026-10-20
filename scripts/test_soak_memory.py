import unittest
from soak_memory import memory_verdict

class MemoryTests(unittest.TestCase):
    def series(self, final=100):
        return [{'seconds':t,'rss':100 if t<82800 else final} for t in range(0,86401,60)]
    def test_stable_boundary_and_growth(self):
        for rss,want in [(100,'STABLE'),(120,'STABLE'),(121,'GROWTH')]:
            self.assertEqual(memory_verdict(self.series(rss),True)['verdict'],want)
    def test_short_failed_paused_and_invalid_are_incomplete(self):
        for samples,complete in [(self.series(),False),(self.series()[:80],True),
                                  (self.series()[0:100]+self.series()[200:],True),
                                  (self.series()+[{'seconds':86401,'rss':float('nan')}],True)]:
            self.assertEqual(memory_verdict(samples,complete)['verdict'],'INCOMPLETE')
    def test_warmup_spike_ignored(self):
        samples=self.series()
        for s in samples:
            if s['seconds']<3600:s['rss']=1000
        self.assertEqual(memory_verdict(samples,True)['growth_percent'],0)

if __name__=='__main__':unittest.main()
