package loopback

import "testing"

func TestHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "127.0.0.1": true, "127.1.2.3": true, "::1": true, "[::1]": true,
		"": false, "0.0.0.0": false, "evil.example": false, "10.0.0.1": false, "foo.localhost": false,
	} {
		if got := Host(host); got != want {
			t.Errorf("Host(%q) = %v, want %v", host, got, want)
		}
	}
}
