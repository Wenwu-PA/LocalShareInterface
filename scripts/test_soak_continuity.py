import unittest
from verify_go_soak import check_workload_gap


class ContinuityTests(unittest.TestCase):
    def test_short_gap_and_boundary(self):
        self.assertEqual(check_workload_gap(105, 100, 120), 5)
        self.assertEqual(check_workload_gap(220, 100, 120), 120)

    def test_stall_invalidates_continuous_run(self):
        with self.assertRaisesRegex(RuntimeError, 'continuous soak invalid'):
            check_workload_gap(221, 100, 120)
        with self.assertRaisesRegex(RuntimeError, 'continuous soak invalid'):
            check_workload_gap(86401, 100, 120)


if __name__ == '__main__':
    unittest.main()
