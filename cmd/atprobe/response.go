package main

import "strings"

// Only complete result lines terminate a command. A product name containing
// OK/ERROR, or a partial read ending in OK, is not an AT result.
func terminalATResponse(text string) (complete, failed bool) {
	end := strings.LastIndexAny(text, "\r\n")
	if end < 0 {
		return false, false
	}
	for _, raw := range strings.FieldsFunc(text[:end], func(r rune) bool { return r == '\r' || r == '\n' }) {
		line := strings.ToUpper(strings.TrimSpace(raw))
		if line == "OK" {
			return true, false
		}
		if line == "ERROR" || strings.HasPrefix(line, "+CME ERROR:") || strings.HasPrefix(line, "+CMS ERROR:") {
			return true, true
		}
	}
	return false, false
}
