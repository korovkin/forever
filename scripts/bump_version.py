#!/usr/bin/python

import os
import sys
import re

lines = open("version.go", "r").read()
current_version = re.match('''.*const VERSION_NUMBER = "([^"]*)".*''', lines, re.DOTALL).groups()
prev_string = current_version[0]
current_version = current_version[0].split(".")
current_version[-1] = "%03d" % (int(current_version[-1])+1)
new_string = ".".join(current_version)
lines = lines.replace(prev_string, new_string)

print("bump_version: => version: ", prev_string, " => ", new_string)

open("version.go", "w").write(lines)
open("_version.txt", "w").write(new_string)
