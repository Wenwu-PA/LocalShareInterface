"""Regression checks for documentation heading anchors."""
import unittest
from check_docs import markdown_anchors


class AnchorTests(unittest.TestCase):
    def test_headings_and_duplicates(self):
        text = '# Title\n## Параметры\n## API: `GET`\n## Title\n<a id="custom"></a>'
        self.assertEqual(markdown_anchors(text), {'title', 'параметры', 'api-get', 'title-1', 'custom'})

    def test_fenced_example_is_not_a_heading(self):
        self.assertEqual(markdown_anchors('```python\n# example\n```\n## Real'), {'real'})


if __name__ == '__main__':
    unittest.main()
