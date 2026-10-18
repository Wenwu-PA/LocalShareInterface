import json
from pathlib import Path
import tempfile
import unittest
from summarize_go_soak import summarize, METRICS


class SoakSummaryTests(unittest.TestCase):
    def test_running_and_completed_reports(self):
        with tempfile.TemporaryDirectory() as temporary:
            folder = Path(temporary)
            result = {'result': 'RUNNING', 'file_bytes': 8, 'full_day': False}
            samples = [dict(seconds=second, cycles=index, **{key: 10+index for key in METRICS})
                       for index, second in enumerate((0, 10, 3600, 86400))]
            (folder/'result.json').write_text(json.dumps(result))
            (folder/'samples.jsonl').write_text(''.join(json.dumps(row)+'\n' for row in samples)+'{"seconds":')
            summary = summarize(folder)
            self.assertFalse(summary['full_day_pass'])
            self.assertEqual(summary['samples'], 4)
            self.assertEqual(summary['hours'][0]['median']['rss'], 10.5)
            self.assertEqual(summary['bytes_each_direction'], 24)
            result.update(result='PASS', full_day=True, seconds=86400)
            (folder/'result.json').write_text(json.dumps(result))
            self.assertTrue(summarize(folder)['full_day_pass'])
            result['result'] = 'FAIL'
            (folder/'result.json').write_text(json.dumps(result))
            self.assertFalse(summarize(folder)['full_day_pass'])
            (folder/'samples.jsonl').write_text('{invalid}\n')
            with self.assertRaises(ValueError):
                summarize(folder)
            (folder/'samples.jsonl').write_text(json.dumps(samples[1])+'\n'+json.dumps(samples[0])+'\n')
            with self.assertRaises(ValueError):
                summarize(folder)


if __name__ == '__main__':
    unittest.main()
