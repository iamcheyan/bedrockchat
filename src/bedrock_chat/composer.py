"""Render each attachment as one editable character in the prompt buffer."""

import re
from collections.abc import Callable, Mapping

from prompt_toolkit.history import InMemoryHistory
from prompt_toolkit.layout.processors import Processor, Transformation, TransformationInput
from prompt_toolkit.layout.utils import explode_text_fragments

from .images import ImageAttachment

# Reserve Supplementary Private Use Area-A for opaque buffer markers. The TUI
# renders labels instead of these characters; ordinary text paste strips them.
MARK_START = 0xF0000
MARK_END = 0xFFFFD
_MARKS = re.compile(f"[{chr(MARK_START)}-{chr(MARK_END)}]")


def plain_text(text: str) -> str:
    return _MARKS.sub("", text)


def markers(text: str) -> list[str]:
    return list(dict.fromkeys(_MARKS.findall(text)))


class TextHistory(InMemoryHistory):
    """Recalling a command or message must never reattach any images."""

    def append_string(self, string: str) -> None:
        text = plain_text(string)
        if text and self.get_strings()[-1:] != [text]:
            super().append_string(text)


class ImageChips(Processor):
    def __init__(self, images: Mapping[str, ImageAttachment], label: Callable[[int], str]):
        self.images = images
        self.label = label

    def apply_transformation(self, ti: TransformationInput) -> Transformation:
        if not any(_MARKS.search(fragment[1]) for fragment in ti.fragments):
            return Transformation(ti.fragments)
        numbers = {marker: i for i, marker in enumerate(self.images, 1)}
        fragments = []
        offsets = [0]
        inverse = []
        for index, (style, char, *rest) in enumerate(explode_text_fragments(ti.fragments)):
            if char in numbers:
                char = self.label(numbers[char])
                style += " reverse"
            elif _MARKS.fullmatch(char):
                char = ""
            fragments.append((style, char, *rest))
            offsets.append(offsets[-1] + len(char))
            inverse.extend([index] * len(char))

        def source_to_display(i: int) -> int:
            if i < 0:
                return i
            return offsets[i] if i < len(offsets) else offsets[-1] + i - (len(offsets) - 1)

        def display_to_source(i: int) -> int:
            if i < 0:
                return i
            return inverse[i] if i < len(inverse) else len(offsets) - 1 + i - len(inverse)

        return Transformation(fragments, source_to_display, display_to_source)
