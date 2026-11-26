package server

import "testing"

// A loopback address needs no warning; any other does.
func TestLoopback(t *testing.T) {
	for addr, want := range map[string]bool{"127.0.0.1:7340": true, "localhost:0": true, "[::1]:80": true, "0.0.0.0:7340": false, "192.168.1.2:80": false, ":7340": false} {
		if got := loopback(addr); got != want {
			t.Errorf("loopback(%q) = %v", addr, got)
		}
	}
}
