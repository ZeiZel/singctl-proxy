#!/usr/bin/env python3
"""Extract main.version from static `go version -m` output."""
import shlex
import sys

version = ""
for line in sys.stdin:
    if "-ldflags" not in line:
        continue
    words = shlex.split(line)
    expanded = []
    for i, word in enumerate(words):
        if word == "-ldflags" and i + 1 < len(words):
            expanded.extend(shlex.split(words[i + 1]))
        elif word.startswith("-ldflags="):
            expanded.extend(shlex.split(word.split("=", 1)[1]))
        else:
            expanded.append(word)
    words = expanded
    for i, word in enumerate(words):
        candidate = words[i + 1] if word == "-X" and i + 1 < len(words) else word[2:] if word.startswith("-X") else word
        if candidate.startswith("main.version="):
            version = candidate.split("=", 1)[1]
print(version)
