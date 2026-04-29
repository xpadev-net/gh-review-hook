package main

import (
	"fmt"
	"os"
	"os/signal"

	"golang.org/x/term"
)

// In raw terminal mode, ONLCR is disabled so \n alone does not produce a
// carriage return. All output in raw mode must use \r\n for newlines.

func renderMenu(targets []installTarget, cursor int) {
	fmt.Print("Select installation target (↑/↓ to move, Enter to confirm, q/Ctrl+C to cancel):\r\n")
	for i, t := range targets {
		if i == cursor {
			fmt.Printf("  > %s\r\n", t.label)
		} else {
			fmt.Printf("    %s\r\n", t.label)
		}
	}
}

func clearMenu(numTargets int) {
	lines := numTargets + 1
	// Move cursor up 'lines' rows, go to column 0, clear to end of screen.
	fmt.Printf("\033[%dA\r\033[0J", lines)
}

func selectTarget(targets []installTarget) (selected int, cancelled bool, err error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return 0, false, fmt.Errorf("stdin is not a terminal; gh-review-hook install requires an interactive terminal")
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return 0, false, fmt.Errorf("cannot enter raw terminal mode: %w", err)
	}
	defer term.Restore(fd, oldState)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		term.Restore(fd, oldState)
		os.Exit(130)
	}()
	defer signal.Stop(sigCh)

	cursor := 0
	renderMenu(targets, cursor)

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf[:1])
		if err != nil || n == 0 {
			return 0, true, nil
		}

		switch buf[0] {
		case 0x03: // Ctrl+C
			fmt.Print("\r\n")
			return 0, true, nil
		case 'q', 'Q':
			fmt.Print("\r\n")
			return 0, true, nil
		case '\r', '\n':
			fmt.Print("\r\n")
			return cursor, false, nil
		case 0x1B: // ESC — start of arrow key sequence
			n, _ = os.Stdin.Read(buf[1:3])
			if n == 2 && buf[1] == '[' {
				switch buf[2] {
				case 'A': // Up arrow
					if cursor > 0 {
						cursor--
					}
				case 'B': // Down arrow
					if cursor < len(targets)-1 {
						cursor++
					}
				}
			}
		}

		clearMenu(len(targets))
		renderMenu(targets, cursor)
	}
}
