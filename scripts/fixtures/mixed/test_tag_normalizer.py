"""Public development checks; the owner also runs its own untouched oracle."""
import unittest

from tag_normalizer import normalize_tags


class TagNormalizerTests(unittest.TestCase):
    def test_normalization(self):
        self.assertEqual(normalize_tags([" Beta ", "alpha", "ALPHA", "", " \t"]),
                         ["alpha", "beta"])

    def test_input_not_mutated(self):
        source = [" B ", "a"]
        self.assertEqual(normalize_tags(source), ["a", "b"])
        self.assertEqual(source, [" B ", "a"])

    def test_non_strings_rejected(self):
        with self.assertRaises(TypeError):
            normalize_tags(["ok", 3])


if __name__ == "__main__":
    unittest.main()
