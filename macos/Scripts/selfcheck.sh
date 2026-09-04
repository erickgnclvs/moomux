#!/usr/bin/env bash
# Runs the assert-based demo() self-checks, through the real app binary.
#
# There is no test target and there cannot be one: neither XCTest nor
# swift-testing exists in a command-line-tools toolchain, both ship inside
# Xcode. So the checks are assert-based functions compiled alongside the code
# they verify, and `Moomux --selftest` runs them.
#
# The build here is DEBUG on purpose, and that is the whole subtlety:
# `swift build -c release` passes -O, and -O deletes every `assert`, so a
# release binary would print "selftest: ok" having verified nothing. SelfTest
# refuses to run in that case rather than reporting a hollow pass — but it is
# this script that makes sure it never has to.
set -euo pipefail

cd "$(dirname "$0")/.."

swift build                                     # debug: -Onone, asserts live
exec "$(swift build --show-bin-path)/Moomux" --selftest
