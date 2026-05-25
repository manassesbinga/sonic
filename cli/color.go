package cli

import (
	"fmt"
	"os"
)

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	blue   = "\033[34m"
	cyanC  = "\033[36m"
)

func color(s, c string) string {
	if c == "" || s == "" {
		return s
	}
	return c + s + reset
}

const magenta = "\033[35m"

func Red(s string) string     { return color(s, red) }
func Green(s string) string   { return color(s, green) }
func Yellow(s string) string  { return color(s, yellow) }
func Blue(s string) string    { return color(s, blue) }
func Cyan(s string) string    { return color(s, cyanC) }
func Magenta(s string) string { return color(s, magenta) }
func Bold(s string) string    { return color(s, bold) }

func success(msg string) {
	fmt.Fprintln(os.Stderr, Green("✓")+" "+msg)
}

func warn(msg string) {
	fmt.Fprintln(os.Stderr, Yellow("⚠")+" "+msg)
}

func info(msg string) {
	fmt.Fprintln(os.Stderr, Cyan("ℹ")+" "+msg)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, Red("✗")+" "+msg)
}
