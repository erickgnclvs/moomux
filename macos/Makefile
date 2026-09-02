CONFIG ?= release
BUNDLE_ID := app.moomux.Moomux
APP := .build/Moomux.app
# Deferred (=) rather than immediate (:=): `dev` sets CONFIG per-target, and :=
# would bake in the release path at parse time.
BINDIR = $(shell swift build -c $(CONFIG) --show-bin-path)
BIN = $(BINDIR)/Moomux

.PHONY: build app run dev selfcheck install shot clean

build:
	swift build -c $(CONFIG)

# The assert-based checks. Deliberately not part of `build`: they must never be
# built with -O, which deletes every assert (see Scripts/selfcheck.sh).
selfcheck:
	bash Scripts/selfcheck.sh

# Wrap the SwiftPM binary in a bundle. There is no Xcode here, so this is the
# app target. The bundle is not optional for anything that wants a bundle
# identifier — notifications and launch-at-login both need one.
#
# Ad-hoc signing (`-`) is fine while nothing depends on a stable designated
# requirement. When launch-at-login or notification authorization lands, switch
# to a stable local identity — an ad-hoc signature changes every build, which
# makes those registrations unreliable.
app: build
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources
	cp $(BIN) $(APP)/Contents/MacOS/Moomux
	cp Resources/Info.plist $(APP)/Contents/Info.plist
	codesign --force --sign - --identifier $(BUNDLE_ID) $(APP)

# ARGS is passed through to the app, e.g. `make dev ARGS="--socket /tmp/mmx.sock"`
# to point at a `moomux serve` other than the default one.
ARGS ?=

# Waiting out the old process is not politeness: `open` against an app that is
# still terminating silently does nothing, which reads as a crash on launch.
run: app
	pkill -x Moomux || true
	@while pgrep -x Moomux >/dev/null; do sleep 0.2; done
	open $(APP) --args $(ARGS)

# A debug bundle for the edit-look-edit loop: seconds instead of the release
# build's minute. Same bundle identifier, so nothing else changes.
dev: CONFIG = debug
dev: app
	pkill -x Moomux || true
	@while pgrep -x Moomux >/dev/null; do sleep 0.2; done
	open $(APP) --args $(ARGS)

install: app
	rm -rf /Applications/Moomux.app
	cp -R $(APP) /Applications/Moomux.app

# A PNG of the running app, for showing a UI change the way the Go side shows
# one with scripts/screenshot.sh.
shot:
	bash Scripts/shot.sh $(OUT)

clean:
	rm -rf .build
