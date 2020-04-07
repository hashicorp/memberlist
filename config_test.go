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
		if err := config.IPAllowed(localV4); err != nil {
			t.Fatalf("IP %s Localhost Should be accepted for LAN", ip)
		}
	}
	config = DefaultWANConfig()
	for _, ip := range tests {
		localV4 := net.ParseIP(ip)
		if err := config.IPAllowed(localV4); err != nil {
			t.Fatalf("IP %s Localhost Should be accepted for WAN", ip)
		}
	}
}

func Test_IsValidAddressOverride(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		allow   []string
		success []string
		fail    []string
	}{
		{
			name:    "Default, nil allows all",
			allow:   nil,
			success: []string{"127.0.0.5", "10.0.0.9", "192.168.1.7", "::1"},
			fail:    []string{},
		},
		{
			name:    "Only IPv4",
			allow:   []string{"0.0.0.0/0"},
			success: []string{"127.0.0.5", "10.0.0.9", "192.168.1.7"},
			fail:    []string{"fe80::38bc:4dff:fe62:b1ae", "::1"},
		},
		{
			name:    "Only IPv6",
			allow:   []string{"::0/0"},
			success: []string{"fe80::38bc:4dff:fe62:b1ae", "::1"},
			fail:    []string{"127.0.0.5", "10.0.0.9", "192.168.1.7"},
		},
		{
			name:    "Only 127.0.0.0/8 and ::1",
			allow:   []string{"::1/128", "127.0.0.0/8"},
			success: []string{"127.0.0.5", "::1"},
			fail:    []string{"::2", "178.250.0.187", "10.0.0.9", "192.168.1.7", "fe80::38bc:4dff:fe62:b1ae"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := DefaultLANConfig()
			var err error
			config.CIDRsAllowed, err = ParseCIDRs(testCase.allow)
			if err != nil {
				t.Fatalf("failed parsing %s", testCase.allow)
			}
			for _, ips := range testCase.success {
				ip := net.ParseIP(ips)
				if err := config.IPAllowed(ip); err != nil {
					t.Fatalf("Test case with %s should pass", ip)
				}
			}
			for _, ips := range testCase.fail {
				ip := net.ParseIP(ips)
				if err := config.IPAllowed(ip); err == nil {
					t.Fatalf("Test case with %s should fail", ip)
				}
			}
		})

	}

}
