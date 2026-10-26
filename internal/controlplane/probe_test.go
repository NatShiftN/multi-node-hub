package controlplane

import "testing"

func TestProbeAddress(t *testing.T) {
	cases := []struct {
		address  string
		remoteIP string
		port     int
		want     string
	}{
		{"node.example.com", "10.0.0.8", 22, "node.example.com:22"},
		{"", "10.0.0.8", 22, "10.0.0.8:22"},
		{"2001:db8::1", "", 22, "[2001:db8::1]:22"},
		{"[2001:db8::1]", "", 22, "[2001:db8::1]:22"},
		{"node.example.com:2222", "", 22, "node.example.com:22"},
		{"", "", 22, ""},
	}
	for _, test := range cases {
		if got := probeAddress(test.address, test.remoteIP, test.port); got != test.want {
			t.Fatalf("probeAddress(%q, %q) = %q, want %q", test.address, test.remoteIP, got, test.want)
		}
	}
}
