package main

import "testing"

func TestTerminalATResponse(t *testing.T) {
	for _, tc := range []struct {
		text             string
		complete, failed bool
	}{
		{"MODEL-OK-ERROR\r\n", false, false},
		{"\r\nOK", false, false},
		{"\r\nOKAY\r\n", false, false},
		{"\r\nOK\r\n", true, false},
		{"\r\nERROR\r\n", true, true},
		{"\r\n+CME ERROR: 3\r\n", true, true},
		{"\r\n+CMS ERROR: 500\r\n", true, true},
	} {
		complete, failed := terminalATResponse(tc.text)
		if complete != tc.complete || failed != tc.failed {
			t.Errorf("%q: got %v/%v", tc.text, complete, failed)
		}
	}
}
