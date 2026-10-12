"""Small incremental VT screen reader for the terminal release smoke (no packages)."""

import codecs
import re
import unicodedata


class TerminalScreen:
    def __init__(self, width, height):
        self.width, self.height = width, height
        self.rows = [[" "] * width for _ in range(height)]
        self.row = self.column = 0
        self.pending = ""
        self.decoder = codecs.getincrementaldecoder("utf-8")("replace")

    @property
    def text(self):
        return "\n".join("".join(row).rstrip() for row in self.rows)

    def feed(self, data):
        text = self.pending + self.decoder.decode(data)
        self.pending = ""
        index = 0
        while index < len(text):
            character = text[index]
            if character == "\x1b":
                if index + 1 == len(text):
                    break
                if text[index + 1] == "[":
                    match = re.match(r"\x1b\[([0-?]*)([ -/]*)([@-~])", text[index:])
                    if not match:
                        break
                    self._csi(match.group(1), match.group(3))
                    index += len(match.group(0))
                    continue
                if text[index + 1] in "]P_":
                    end = re.search(r"\x07|\x1b\\", text[index + 2:])
                    if not end:
                        break
                    index += 2 + end.end()
                    continue
                if text[index + 1] in "()":
                    if index + 2 == len(text):
                        break
                    index += 3
                    continue
                index += 2
                continue
            if character == "\r":
                self.column = 0
            elif character == "\n":
                self._linefeed()
            elif character == "\b":
                self.column = max(0, self.column - 1)
            elif character == "\t":
                self.column = min(self.width - 1, (self.column // 8 + 1) * 8)
            elif ord(character) >= 32:
                cells = 0 if unicodedata.combining(character) else 2 if unicodedata.east_asian_width(character) in "WF" else 1
                if cells == 0:
                    self.rows[self.row][max(0, self.column - 1)] += character
                else:
                    if self.column + cells > self.width:
                        self.column = 0
                        self._linefeed()
                    self.rows[self.row][self.column] = character
                    if cells == 2:
                        self.rows[self.row][self.column + 1] = ""
                    self.column += cells
            index += 1
        self.pending = text[index:]

    def _linefeed(self):
        self.column = min(self.width - 1, self.column)
        self.row += 1
        if self.row >= self.height:
            self.rows.pop(0)
            self.rows.append([" "] * self.width)
            self.row = self.height - 1

    def _csi(self, parameters, command):
        if command not in "HfGdABCDJKXSThP@Z":
            return
        self.column = min(self.width - 1, self.column)
        values = [int(value or "0") for value in parameters.lstrip("?<=>").split(";")]
        count = values[0] or 1
        if command in "Hf":
            self.row = max(0, min(self.height - 1, count - 1))
            self.column = max(0, min(self.width - 1, (values[1] if len(values) > 1 and values[1] else 1) - 1))
        elif command == "G":
            self.column = max(0, min(self.width - 1, count - 1))
        elif command == "d":
            self.row = max(0, min(self.height - 1, count - 1))
        elif command == "Z":
            self.column = max(0, ((self.column - 1) // 8 - count + 1) * 8)
        elif command in "ABCD":
            if command == "A":
                self.row = max(0, self.row - count)
            elif command == "B":
                self.row = min(self.height - 1, self.row + count)
            elif command == "C":
                self.column = min(self.width - 1, self.column + count)
            else:
                self.column = max(0, self.column - count)
        elif command in "JKX":
            mode = values[0]
            if command == "X":
                for column in range(self.column, min(self.width, self.column + count)):
                    self.rows[self.row][column] = " "
            else:
                for row in range(self.height):
                    for column in range(self.width):
                        before = (row, column) <= (self.row, self.column)
                        after = (row, column) >= (self.row, self.column)
                        if (command == "J" or row == self.row) and (mode == 2 or mode == 0 and after or mode == 1 and before):
                            self.rows[row][column] = " "
        elif command in "ST":
            count = min(count, self.height)
            blank = [[" "] * self.width for _ in range(count)]
            self.rows = self.rows[count:] + blank if command == "S" else blank + self.rows[:self.height - count]
        elif command in "P@":
            count = min(count, self.width - self.column)
            row = self.rows[self.row]
            if command == "P":
                self.rows[self.row] = row[:self.column] + row[self.column + count:] + [" "] * count
            else:
                self.rows[self.row] = (row[:self.column] + [" "] * count + row[self.column:])[:self.width]
        elif command == "h" and parameters == "?1049":
            self.rows = [[" "] * self.width for _ in range(self.height)]
            self.row = self.column = 0
