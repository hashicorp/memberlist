package memberlist

import (
	"net"
	"testing"
)

func Test_IsValidAddressDefaults(t *testing.T) {
	tests := []string{
		"127.0.0.1",
		"127.0.0.5",
		"10.0.0.9",
		"172.16.0.7",
		"192.168.2.1",
		"fe80::aede:48ff:fe00:1122",
		"::1",
	}
	config := DefaultLANConfig()
	for _, ip := range tests {
		localV4 := net.ParseIP(ip)
		if err := config.IpAllowed(localV4); err != nil {
			t.Fatalf("IP %s Localhost Should be accepted for LAN", ip)
		}
	}
	config = DefaultWANConfig()
	for _, ip := range tests {
		localV4 := net.ParseIP(ip)
		if err := config.IpAllowed(localV4); err != nil {
			t.Fatalf("IP %s Localhost Should be accepted for WAN", ip)
		}
	}
}

func Test_IsValidAddressOverride(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		allow   []string
		deny    []string
		success []string
		fail    []string
	}{
		{
			name:    "Only IPv4",
			allow:   []string{"0.0.0.0/0"},
			deny:    []string{},
			success: []string{"127.0.0.5", "10.0.0.9", "192.168.1.7"},
			fail:    []string{"fe80::38bc:4dff:fe62:b1ae"},
		},
		{
			name:    "Only IPv6",
			allow:   []string{"::0/0"},
			deny:    []string{},
			success: []string{"fe80::38bc:4dff:fe62:b1ae"},
			fail:    []string{"127.0.0.5", "10.0.0.9", "192.168.1.7"},
		},
		{
			name:    "All OK but not localhost IPv4/6",
			allow:   []string{"0.0.0.0/0", "::0/0"},
			deny:    []string{"127.0.0.0/8", "::1/128"},
			success: []string{"fe80::38bc:4dff:fe62:b1ae", "10.0.0.9", "192.168.1.7"},
			fail:    []string{"::1", "127.0.0.5"},
		},
		{
			name:    "Only 192.168.5.0 IPv4/6 without 192.168.5.1",
			allow:   []string{"192.168.5.0/24", "::ffff:192.0.5.0/120"},
			deny:    []string{"192.168.5.1/32", "::ffff:192.0.5.1/128"},
			success: []string{"192.168.5.2", "::ffff:192.0.5.2", "192.168.5.3", "::ffff:192.0.5.3"},
			fail:    []string{"8.8.8.8", "::1", "127.0.0.5", "192.168.5.1", "::ffff:192.0.5.1", "192.168.1.2", "::ffff:192.0.1.2"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := DefaultLANConfig()
			var err error
			config.CidrsAllowed, err = ParseCIDRs(testCase.allow)
			if err != nil {
				t.Fatalf("failed parsing %s", testCase.allow)
			}
			config.CidrsDenied, err = ParseCIDRs(testCase.deny)
			if err != nil {
				t.Fatalf("failed parsing %s", testCase.deny)
			}
			for _, ips := range testCase.success {
				ip := net.ParseIP(ips)
				if err := config.IpAllowed(ip); err != nil {
					t.Fatalf("Test case with %s should pass", ip)
				}
			}
			for _, ips := range testCase.fail {
				ip := net.ParseIP(ips)
				if err := config.IpAllowed(ip); err == nil {
					t.Fatalf("Test case with %s should fail", ip)
				}
			}
		})

	}

}
