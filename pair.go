package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"rsc.io/qr"

	"github.com/erickgnclvs/moomux/internal/ipc"
)

// errNoCore is the one failure the core can't word for `moomux pair`,
// because there is no core to ask. The Mac app shows "Not connected" for the
// same thing, so this copy is the CLI's own.
//
//lint:ignore ST1005 user-facing sentence, shown verbatim
var errNoCore = errors.New("No moomux core is running. Start it (`brew services start moomux`, or `moomux serve`), then try again.")

// errOldCore is the other: a core from before Pair — `brew upgrade` without
// a service restart — answers `unknown method "Pair"`, and can't word
// anything about a method it doesn't have. CLI-only like errNoCore; the Mac
// app says it in its own words.
//
//lint:ignore ST1005 user-facing sentence, shown verbatim
var errOldCore = errors.New("The running moomux core is older than this command. Restart it (`brew services restart moomux`), then try again.")

// runPair implements `moomux pair`: ask the running core to listen on the
// tailnet, if it isn't already, and print the QR code an iPhone's Camera
// scans to reach it. Every check — Tailscale installed, on, signed in as a
// person, the listener up — is the core's, and so is the wording; the
// errors it returns are whole sentences, which main prints unprefixed.
func runPair(args []string) error {
	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	sock := fs.String("socket", ipc.DefaultSocket(home), "unix socket of a running `moomux serve`")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := (&ipc.Client{Socket: *sock}).Pair()
	if err != nil {
		// Only the dial: a core that answered and then failed has said why.
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return errNoCore
		}
		// Except a core too old to know the method, which can't.
		if strings.HasPrefix(err.Error(), "unknown method") {
			return errOldCore
		}
		return err
	}
	return printPairing(os.Stdout, p)
}

// printPairing prints the QR code, then the link as text on a line of its
// own — it works tapped from Messages or Notes too, for when a camera isn't
// handy — then who the phone has to be signed in as, and, only when this
// call opened the listener, who can now reach it.
func printPairing(w io.Writer, p ipc.Pairing) error {
	code, err := qr.Encode(p.Link, qr.M)
	if err != nil {
		return err
	}
	renderQR(w, code.Size, code.Black)
	_, err = fmt.Fprintf(w, "\n%s\nScan with your iPhone's Camera. On the iPhone, sign in to Tailscale as %s.\n", p.Link, p.Owner)
	if err != nil || !p.TurnedOn {
		return err
	}
	_, err = fmt.Fprintf(w, "Devices on your tailnet signed in as %s can now reach moomux on this computer.\n", p.Owner)
	return err
}

// qrQuietZone is the white border, in modules, on each side of the code.
// The QR spec asks for 4; 2 keeps the code a few rows shorter in a
// terminal, and is what was checked: a code rendered this way decoded back
// to the exact link through Apple's CIDetector. A real iPhone Camera scan
// hasn't confirmed it yet — if one fails, 4 is the first thing to try.
const qrQuietZone = 2

// renderQR draws a size×size code with half blocks, two module rows per
// text row: a terminal cell is about twice as tall as it is wide, so that
// draws square modules. The colours are explicit, black on bright white,
// rather than the terminal's own: a dark theme would draw the code light on
// dark, inverted, which not every reader decodes.
func renderQR(w io.Writer, size int, black func(x, y int) bool) {
	on := func(x, y int) bool { return x >= 0 && x < size && y >= 0 && y < size && black(x, y) }
	var b strings.Builder
	for y := -qrQuietZone; y < size+qrQuietZone; y += 2 {
		b.WriteString("\x1b[30;107m")
		for x := -qrQuietZone; x < size+qrQuietZone; x++ {
			// A code's side is odd, so the last row's bottom half falls
			// past the quiet zone and reads as white: a little more border.
			switch top, bottom := on(x, y), on(x, y+1); {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteByte(' ')
			}
		}
		b.WriteString("\x1b[0m\n")
	}
	_, _ = io.WriteString(w, b.String())
}
