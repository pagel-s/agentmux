"""Owner-side oracle; never copied into participant workspaces."""
import importlib.util
import json
from pathlib import Path


def main():
    path = Path(__file__).with_name("tag_normalizer.py")
    spec = importlib.util.spec_from_file_location("submitted_implementation", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    normalize = module.normalize_tags
    cases = [
        ([], []),
        (["Alpha", " alpha ", "BETA", " beta "], ["alpha", "beta"]),
        (["", " ", "\t\n", " b ", "a"], ["a", "b"]),
        (["Z", "a", "z", "A"], ["a", "z"]),
        ([" naïve ", "NAÏVE", " café "], ["café", "naïve"]),
        ((" x ", "X", " y "), ["x", "y"]),
    ]
    for values, expected in cases:
        original = list(values)
        actual = normalize(values)
        assert actual == expected, "normalization result mismatch"
        assert list(values) == original, "input was mutated"
        assert isinstance(actual, list), "return type must be list"
    for invalid in (None, 7, b"bytes", object()):
        try:
            normalize(["valid", invalid])
        except TypeError:
            pass
        else:
            raise AssertionError("non-string elements must raise TypeError")
    assert normalize(value for value in (" B ", "a", "A", "")) == ["a", "b"], "finite generators must work"
    assert normalize(["one"]) is not normalize(["one"]), "return a fresh list"
    print(json.dumps({"ok": True, "normalization_cases": len(cases),
                      "type_error_cases": 4, "finite_generator": True, "fresh_output": True}))


if __name__ == "__main__":
    main()
