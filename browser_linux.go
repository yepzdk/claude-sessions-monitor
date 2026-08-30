//go:build linux

package main

// browserOpener is the freedesktop tool that hands a URL to the default
// browser. It ships in xdg-utils, which a minimal install may not have, and
// that is the failure openBrowser reports rather than swallows.
const browserOpener = "xdg-open"
