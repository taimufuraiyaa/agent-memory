import unittest

from terminal_screen import TerminalScreen


class TerminalScreenTests(unittest.TestCase):
    def test_partial_redraw_preserves_unchanged_cells(self):
        screen = TerminalScreen(20, 3)
        screen.feed(b"Records\r\x1b[3Gading\x1b[K")
        self.assertEqual(screen.text.splitlines()[0], "Reading")

    def test_chunked_escape_utf8_and_wide_cells(self):
        screen = TerminalScreen(20, 3)
        data = "\x1b[2;3H界面\x1b[2;7H!".encode()
        for byte in data:
            screen.feed(bytes([byte]))
        self.assertEqual(screen.text.splitlines()[1], "  界面!")

    def test_title_sequences_do_not_enter_the_screen(self):
        screen = TerminalScreen(20, 3)
        screen.feed(b"\x1b]0;Window title\x07Visible")
        self.assertEqual(screen.text.splitlines()[0], "Visible")

    def test_private_keyboard_and_color_protocols_do_not_change_text(self):
        screen = TerminalScreen(20, 3)
        screen.feed(b"\x1b[=0u\x1b[38:2:100:100:100mReading")
        self.assertEqual(screen.text.splitlines()[0], "Reading")

    def test_delete_and_insert_characters_shift_cells_without_moving_cursor(self):
        screen = TerminalScreen(20, 3)
        screen.feed(b"abcdefghij\r\x1b[4G\x1b[3P")
        self.assertEqual(screen.text.splitlines()[0], "abcghij")
        screen.feed(b"\x1b[2@XY")
        self.assertEqual(screen.text.splitlines()[0], "abcXYghij")

    def test_cursor_motion_cancels_pending_last_column_wrap(self):
        screen = TerminalScreen(5, 3)
        screen.feed(b"abcde\x1b[DZ")
        self.assertEqual(screen.text.splitlines()[0], "abcZe")

    def test_backward_tabs_follow_renderer_eight_column_stops(self):
        screen = TerminalScreen(30, 3)
        screen.feed(b"abc\ttail\x1b[ZXYZ")
        self.assertEqual(screen.text.splitlines()[0], "abc     XYZl")
        screen.feed(b"\x1b[2Z!")
        self.assertEqual(screen.text.splitlines()[0], "!bc     XYZl")


if __name__ == "__main__":
    unittest.main()
