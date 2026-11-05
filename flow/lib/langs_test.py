"""Tests for langs.py. Run from lib/: python3 -m unittest -q langs_test.py"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from langs import COMMON_NAMES, DEF_RES, EXTENSIONS  # noqa: E402


def defined(line: str) -> str | None:
    """The name the first matching DEF_RES pattern gives a line, as plan_premises reads it."""
    for pattern in DEF_RES:
        match = pattern.match(line.strip())
        if match:
            return match.group(1)
    return None


class GoTest(unittest.TestCase):
    def test_go_sources_and_their_neighbours_read_as_paths(self):
        for ext in ("go", "mod", "sum", "lua"):
            self.assertIn(ext, EXTENSIONS, ext)

    def test_top_level_go_items_are_definitions(self):
        cases = {
            "func Diff(a, b string) string {": "Diff",
            "func Map[T any](xs []T) []T {": "Map",
            "type Session struct {": "Session",
            "type Option func(*Config)": "Option",
            'var ErrClosed = errors.New("closed")': "ErrClosed",
            "const maxPanes = 8": "maxPanes",
        }
        for line, name in cases.items():
            self.assertEqual(defined(line), name, line)

    def test_methods_and_grouped_declarations_are_not(self):
        for line in ("func (s *Session) String() string {", "type (", "var (", "const (", "func(x int) {"):
            self.assertIsNone(defined(line), line)

    def test_init_is_too_common_to_mean_a_collision(self):
        self.assertEqual(defined("func init() {"), "init")
        self.assertIn("init", COMMON_NAMES)


if __name__ == "__main__":
    unittest.main()
