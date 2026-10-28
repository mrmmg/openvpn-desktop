#!/bin/sh
# Prints the app version: $VERSION if set, otherwise derived from the latest git tag.
v="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null)}"
echo "${v#v}"
