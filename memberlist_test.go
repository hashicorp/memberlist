// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package memberlist

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	iretry "github.com/hashicorp/memberlist/internal/retry"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var bindLock sync.Mutex
var bindNum byte = 10

func getBindAddrNet(network byte) net.IP {
	bindLock.Lock()
	defer bindLock.Unlock()

	result := net.IPv4(127, 0, network, bindNum)
	bindNum++
	//nolint:staticcheck // SA4003: no value of type byte is greater than math.MaxUint8
	if bindNum > 255 {
		bindNum = 10
	}

	return result
}

func getBindAddr() net.IP {
	return getBindAddrNet(0)
}

func testConfigNet(tb testing.TB, network byte) *Config {
	tb.Helper()

	config := DefaultLANConfig()
	config.BindAddr = getBindAddrNet(network).String()
	config.Name = config.BindAddr
	config.BindPort = 0 // choose free port
	config.RequireNodeNames = true
	config.Logger = log.New(os.Stderr, config.Name, log.LstdFlags)
	return config
}

func testConfig(tb testing.TB) *Config {
	return testConfigNet(tb, 0)
}

func yield() {
	time.Sleep(250 * time.Millisecond)
}

type MockDelegate struct {
	mu          sync.Mutex
	meta        []byte
	msgs        [][]byte
	broadcasts  [][]byte
	state       []byte
	remoteState []byte
}

func (m *MockDelegate) setMeta(meta []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.meta = meta
}

func (m *MockDelegate) setState(state []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = state
}

func (m *MockDelegate) setBroadcasts(broadcasts [][]byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broadcasts = broadcasts
}

func (m *MockDelegate) getRemoteState() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]byte, len(m.remoteState))
	copy(out, m.remoteState)
	return out
}

func (m *MockDelegate) getMessages() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([][]byte, len(m.msgs))
	for i, msg := range m.msgs {
		out[i] = make([]byte, len(msg))
		copy(out[i], msg)
	}
	return out
}

func (m *MockDelegate) NodeMeta(limit int) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.meta
}

func (m *MockDelegate) NotifyMsg(msg []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp := make([]byte, len(msg))
	copy(cp, msg)
	m.msgs = append(m.msgs, cp)
}

func (m *MockDelegate) GetBroadcasts(overhead, limit int) [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	b := m.broadcasts
	m.broadcasts = nil
	return b
}

func (m *MockDelegate) LocalState(join bool) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.state
}

func (m *MockDelegate) MergeRemoteState(s []byte, join bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.remoteState = s
}

func GetMemberlist(tb testing.TB, f func(c *Config)) *Memberlist {
	c := testConfig(tb)
	c.BindPort = 0 // assign a free port
	if f != nil {
		f(c)
	}

	m, err := newMemberlist(c)
	require.NoError(tb, err)
	return m
}

func TestDefaultLANConfig_protocolVersion(t *testing.T) {
	c := DefaultLANConfig()
	if c.ProtocolVersion != ProtocolVersion2Compatible {
		t.Fatalf("should be max: %d", c.ProtocolVersion)
	}
}

func TestCreate_protocolVersion(t *testing.T) {
	cases := []struct {
		name    string
		version uint8
		err     bool
	}{
		{"min", ProtocolVersionMin, false},
		{"max", ProtocolVersionMax, false},
		// TODO(mitchellh): uncommon when we're over 0
		//{"uncommon", ProtocolVersionMin - 1, true},
		{"max+1", ProtocolVersionMax + 1, true},
		{"min-1", ProtocolVersionMax - 1, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultLANConfig()
			c.BindAddr = getBindAddr().String()
			c.ProtocolVersion = tc.version

			m, err := Create(c)
			if err == nil {
				require.NoError(t, m.Shutdown())
			}

			if tc.err && err == nil {
				t.Fatalf("Should've failed with version: %d", tc.version)
			} else if !tc.err && err != nil {
				t.Fatalf("Version '%d' error: %s", tc.version, err)
			}
		})
	}
}

func TestCreate_secretKey(t *testing.T) {
	cases := []struct {
		name string
		key  []byte
		err  bool
	}{
		{"size-0", make([]byte, 0), false},
		{"abc", []byte("abc"), true},
		{"size-16", make([]byte, 16), false},
		{"size-38", make([]byte, 38), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultLANConfig()
			c.BindAddr = getBindAddr().String()
			c.SecretKey = tc.key

			m, err := Create(c)
			if err == nil {
				require.NoError(t, m.Shutdown())
			}

			if tc.err && err == nil {
				t.Fatalf("Should've failed with key: %#v", tc.key)
			} else if !tc.err && err != nil {
				t.Fatalf("Key '%#v' error: %s", tc.key, err)
			}
		})
	}
}

func TestCreate_secretKeyEmpty(t *testing.T) {
	c := DefaultLANConfig()
	c.BindAddr = getBindAddr().String()
	c.SecretKey = make([]byte, 0)

	m, err := Create(c)
	require.NoError(t, err)
	defer func() {
		if err := m.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	if m.config.EncryptionEnabled() {
		t.Fatalf("Expected encryption to be disabled")
	}
}

func TestCreate_checkBroadcastQueueMetrics(t *testing.T) {
	sink := registerInMemorySink(t)
	c := DefaultLANConfig()
	c.QueueCheckInterval = 1 * time.Second
	c.BindAddr = getBindAddr().String()
	c.SecretKey = make([]byte, 0)

	m, err := Create(c)
	require.NoError(t, err)
	defer func() {
		if err := m.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	time.Sleep(3 * time.Second)

	sampleName := "consul.usage.test.memberlist.queue.broadcasts"
	verifySampleExists(t, sampleName, sink)
}

func TestCreate_keyringOnly(t *testing.T) {
	c := DefaultLANConfig()
	c.BindAddr = getBindAddr().String()

	keyring, err := NewKeyring(nil, make([]byte, 16))
	require.NoError(t, err)
	c.Keyring = keyring

	m, err := Create(c)
	require.NoError(t, err)
	defer func() {
		if err := m.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	if !m.config.EncryptionEnabled() {
		t.Fatalf("Expected encryption to be enabled")
	}
}

func TestCreate_keyringAndSecretKey(t *testing.T) {
	c := DefaultLANConfig()
	c.BindAddr = getBindAddr().String()

	keyring, err := NewKeyring(nil, make([]byte, 16))
	require.NoError(t, err)
	c.Keyring = keyring
	c.SecretKey = []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}

	m, err := Create(c)
	require.NoError(t, err)
	defer func() {
		if err := m.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	if !m.config.EncryptionEnabled() {
		t.Fatalf("Expected encryption to be enabled")
	}

	ringKeys := c.Keyring.GetKeys()
	if !bytes.Equal(c.SecretKey, ringKeys[0]) {
		t.Fatalf("Unexpected primary key %v", ringKeys[0])
	}
}

func TestCreate_invalidLoggerSettings(t *testing.T) {
	c := DefaultLANConfig()
	c.BindAddr = getBindAddr().String()
	c.Logger = log.New(io.Discard, "", log.LstdFlags)
	c.LogOutput = io.Discard

	m, err := Create(c)
	if err == nil {
		require.NoError(t, m.Shutdown())
		t.Fatal("Memberlist should not allow both LogOutput and Logger to be set, but it did not raise an error")
	}
}

func TestCreate(t *testing.T) {
	c := testConfig(t)
	c.ProtocolVersion = ProtocolVersionMin
	c.DelegateProtocolVersion = 13
	c.DelegateProtocolMin = 12
	c.DelegateProtocolMax = 24

	m, err := Create(c)
	require.NoError(t, err)
	defer func() {
		if err := m.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	yield()

	members := m.Members()
	if len(members) != 1 {
		t.Fatalf("bad number of members")
	}

	if members[0].PMin != ProtocolVersionMin {
		t.Fatalf("bad: %#v", members[0])
	}

	if members[0].PMax != ProtocolVersionMax {
		t.Fatalf("bad: %#v", members[0])
	}

	if members[0].PCur != c.ProtocolVersion {
		t.Fatalf("bad: %#v", members[0])
	}

	if members[0].DMin != c.DelegateProtocolMin {
		t.Fatalf("bad: %#v", members[0])
	}

	if members[0].DMax != c.DelegateProtocolMax {
		t.Fatalf("bad: %#v", members[0])
	}

	if members[0].DCur != c.DelegateProtocolVersion {
		t.Fatalf("bad: %#v", members[0])
	}
}

func TestMemberList_CreateShutdown(t *testing.T) {
	m := GetMemberlist(t, nil)
	m.schedule()
	require.NoError(t, m.Shutdown())
}

func TestMemberList_ResolveAddr(t *testing.T) {
	m := GetMemberlist(t, nil)
	defer func() {
		if err := m.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	defaultPort := uint16(m.config.BindPort)

	type testCase struct {
		name           string
		in             string
		expectErr      bool
		ignoreExpectIP bool
		expect         []ipPort
	}

	baseCases := []testCase{
		{
			name:           "localhost",
			in:             "localhost",
			ignoreExpectIP: true,
			expect: []ipPort{
				{port: defaultPort},
			},
		},
		{
			name: "ipv6 pair",
			in:   "[::1]:80",
			expect: []ipPort{
				{ip: net.IPv6loopback, port: 80},
			},
		},
		{
			name: "ipv6 non-pair",
			in:   "[::1]",
			expect: []ipPort{
				{ip: net.IPv6loopback, port: defaultPort},
			},
		},
		{
			name:      "hostless port",
			in:        ":80",
			expectErr: true,
		},
		{
			name:           "hostname port combo",
			in:             "localhost:80",
			ignoreExpectIP: true,
			expect: []ipPort{
				{port: 80},
			},
		},
		{
			name:      "too high port",
			in:        "localhost:80000",
			expectErr: true,
		},
		{
			name: "ipv4 port combo",
			in:   "127.0.0.1:80",
			expect: []ipPort{
				{ip: net.IPv4(127, 0, 0, 1), port: 80},
			},
		},
		{
			name: "ipv6 port combo",
			in:   "[2001:db8:a0b:12f0::1]:80",
			expect: []ipPort{
				{
					ip:   net.IP{0x20, 0x01, 0x0d, 0xb8, 0x0a, 0x0b, 0x12, 0xf0, 0, 0, 0, 0, 0, 0, 0, 0x1},
					port: 80,
				},
			},
		},
		{
			name:      "ipv4 port combo with empty tag",
			in:        "/127.0.0.1:80",
			expectErr: true,
		},
		{
			name: "ipv4 only",
			in:   "127.0.0.1",
			expect: []ipPort{
				{ip: net.IPv4(127, 0, 0, 1), port: defaultPort},
			},
		},
		{
			name: "ipv6 only",
			in:   "[2001:db8:a0b:12f0::1]",
			expect: []ipPort{
				{
					ip:   net.IP{0x20, 0x01, 0x0d, 0xb8, 0x0a, 0x0b, 0x12, 0xf0, 0, 0, 0, 0, 0, 0, 0, 0x1},
					port: defaultPort,
				},
			},
		},
	}

	// explode the cases to include tagged versions of everything
	var cases []testCase
	for _, tc := range baseCases {
		cases = append(cases, tc)
		if !strings.Contains(tc.in, "/") { // don't double tag already tagged cases
			tc2 := testCase{
				name:           tc.name + " (tagged)",
				in:             "foo.bar/" + tc.in,
				expectErr:      tc.expectErr,
				ignoreExpectIP: tc.ignoreExpectIP,
			}
			for _, ipp := range tc.expect {
				tc2.expect = append(tc2.expect, ipPort{
					ip:       ipp.ip,
					port:     ipp.port,
					nodeName: "foo.bar",
				})
			}
			cases = append(cases, tc2)
		}
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := m.resolveAddr(tc.in)
			if tc.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				if tc.ignoreExpectIP {
					if len(got) > 1 {
						got = got[0:1]
					}
					for i := 0; i < len(got); i++ {
						got[i].ip = nil
					}
				}
				require.Equal(t, tc.expect, got)
			}
		})
	}
}

type dnsHandler struct {
	t *testing.T
}

func (h dnsHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	if len(r.Question) != 1 {
		h.t.Fatalf("bad: %#v", r.Question)
	}

	name := "join.service.consul."
	question := r.Question[0]
	if question.Name != name || question.Qtype != dns.TypeANY {
		h.t.Fatalf("bad: %#v", question)
	}

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true
	m.RecursionAvailable = false
	m.Answer = append(m.Answer, &dns.A{
		Hdr: dns.RR_Header{
			Name:   name,
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET},
		A: net.ParseIP("127.0.0.1"),
	})
	m.Answer = append(m.Answer, &dns.AAAA{
		Hdr: dns.RR_Header{
			Name:   name,
			Rrtype: dns.TypeAAAA,
			Class:  dns.ClassINET},
		AAAA: net.ParseIP("2001:db8:a0b:12f0::1"),
	})
	if err := w.WriteMsg(m); err != nil {
		h.t.Fatalf("err: %v", err)
	}
}

func TestMemberList_ResolveAddr_TCP_First(t *testing.T) {
	bind := "127.0.0.1:8600"

	var wg sync.WaitGroup
	wg.Add(1)
	server := &dns.Server{
		Addr:              bind,
		Handler:           dnsHandler{t},
		Net:               "tcp",
		NotifyStartedFunc: wg.Done,
	}
	defer func() {
		if err := server.Shutdown(); err != nil {
			t.Fatalf("err: %v", err)
		}
	}()

	go func() {
		if err := server.ListenAndServe(); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			t.Errorf("err: %v", err)
		}
	}()
	wg.Wait()

	tmpFile, err := os.CreateTemp("", "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer func() {
		if err := os.Remove(tmpFile.Name()); err != nil {
			t.Fatalf("err: %v", err)
		}
	}()

	content := []byte(fmt.Sprintf("nameserver %s", bind))
	if _, err := tmpFile.Write(content); err != nil {
		t.Fatalf("err: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("err: %v", err)
	}

	m := GetMemberlist(t, func(c *Config) {
		c.DNSConfigPath = tmpFile.Name()
	})
	defer func() {
		if err := m.setAlive(); err != nil {
			t.Fatal(err)
		}
	}()
	m.schedule()
	defer func() {
		if err := m.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	// Try with and without the trailing dot.
	hosts := []string{
		"join.service.consul.",
		"join.service.consul",
	}
	for _, host := range hosts {
		ips, err := m.resolveAddr(host)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		port := uint16(m.config.BindPort)
		expected := []ipPort{
			// Go now parses IPs like this and returns IP4-mapped IPv6 address.
			// Confusingly if you print it you see the same as the input since
			// IP.String converts IP4-mapped addresses back to dotted decimal notation
			// but the underlying IP bytes don't compare as equal to the actual IPv4
			// bytes the resolver will get from DNS.
			ipPort{ip: net.ParseIP("127.0.0.1").To4(), port: port, nodeName: ""},
			ipPort{ip: net.ParseIP("2001:db8:a0b:12f0::1"), port: port, nodeName: ""},
		}
		require.Equal(t, expected, ips)
	}
}

func TestMemberList_Members(t *testing.T) {
	n1 := &Node{Name: "test"}
	n2 := &Node{Name: "test2"}
	n3 := &Node{Name: "test3"}

	m := &Memberlist{}
	nodes := []*nodeState{
		&nodeState{Node: *n1, State: StateAlive},
		&nodeState{Node: *n2, State: StateDead},
		&nodeState{Node: *n3, State: StateSuspect},
	}
	m.nodes = nodes

	members := m.Members()
	if !reflect.DeepEqual(members, []*Node{n1, n3}) {
		t.Fatalf("bad members")
	}
}

func TestMemberlist_Join(t *testing.T) {
	c1 := testConfig(t)
	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	// Create a second node
	c2 := testConfig(t)
	c2.BindPort = bindPort

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	num, err := m2.Join([]string{m1.config.Name + "/" + m1.config.BindAddr})
	if num != 1 {
		t.Fatalf("unexpected 1: %d", num)
	}
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}

	// Check the hosts
	if len(m2.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}
	if m2.estNumNodes() != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}
}

func TestMemberlist_Join_with_Labels(t *testing.T) {
	testMemberlist_Join_with_Labels(t, nil)
}
func TestMemberlist_Join_with_Labels_and_Encryption(t *testing.T) {
	secretKey := TestKeys[0]
	testMemberlist_Join_with_Labels(t, secretKey)
}
func testMemberlist_Join_with_Labels(t *testing.T, secretKey []byte) {
	c1 := testConfig(t)
	c1.Label = "blah"
	c1.SecretKey = secretKey
	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	// Create a second node
	c2 := testConfig(t)
	c2.Label = "blah"
	c2.BindPort = bindPort
	c2.SecretKey = secretKey
	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	checkHost := func(t *testing.T, m *Memberlist, expected int) {
		assert.Equal(t, expected, len(m.Members()))
		assert.Equal(t, expected, m.estNumNodes())
	}

	runStep(t, "same label can join", func(t *testing.T) {
		num, err := m2.Join([]string{m1.config.Name + "/" + m1.config.BindAddr})
		require.NoError(t, err)
		require.Equal(t, 1, num)

		// Check the hosts
		checkHost(t, m2, 2)
		checkHost(t, m1, 2)
	})

	// Create a third node that uses no label
	c3 := testConfig(t)
	c3.Label = ""
	c3.BindPort = bindPort
	c3.SecretKey = secretKey
	m3, err := Create(c3)
	require.NoError(t, err)
	defer func() {
		if err := m3.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()
	runStep(t, "no label cannot join", func(t *testing.T) {
		_, err := m3.Join([]string{m1.config.Name + "/" + m1.config.BindAddr})
		require.Error(t, err)

		// Check the failed host
		checkHost(t, m3, 1)
		// Check the existing hosts
		checkHost(t, m2, 2)
		checkHost(t, m1, 2)
	})

	// Create a fourth node that uses a mismatched label
	c4 := testConfig(t)
	c4.Label = "not-blah"
	c4.BindPort = bindPort
	c4.SecretKey = secretKey
	m4, err := Create(c4)
	require.NoError(t, err)
	defer func() {
		if err := m4.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	runStep(t, "mismatched label cannot join", func(t *testing.T) {
		_, err := m4.Join([]string{m1.config.Name + "/" + m1.config.BindAddr})
		require.Error(t, err)

		// Check the failed host
		checkHost(t, m4, 1)
		// Check the previous failed host
		checkHost(t, m3, 1)
		// Check the existing hosts
		checkHost(t, m2, 2)
		checkHost(t, m1, 2)
	})
}

func TestMemberlist_JoinDifferentNetworksUniqueMask(t *testing.T) {
	c1 := testConfigNet(t, 0)
	c1.CIDRsAllowed, _ = ParseCIDRs([]string{"127.0.0.0/8"})
	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	// Create a second node
	c2 := testConfigNet(t, 1)
	c2.CIDRsAllowed, _ = ParseCIDRs([]string{"127.0.0.0/8"})
	c2.BindPort = bindPort

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	num, err := m2.Join([]string{m1.config.Name + "/" + m1.config.BindAddr})
	if num != 1 {
		t.Fatalf("unexpected 1: %d", num)
	}
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}

	// Check the hosts
	if len(m2.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}
	if m2.estNumNodes() != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}
}

func TestMemberlist_JoinDifferentNetworksMultiMasks(t *testing.T) {
	c1 := testConfigNet(t, 0)
	c1.CIDRsAllowed, _ = ParseCIDRs([]string{"127.0.0.0/24", "127.0.1.0/24"})
	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	// Create a second node
	c2 := testConfigNet(t, 1)
	c2.CIDRsAllowed, _ = ParseCIDRs([]string{"127.0.0.0/24", "127.0.1.0/24"})
	c2.BindPort = bindPort

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	err = joinAndTestMemberShip(t, m2, []string{m1.config.Name + "/" + m1.config.BindAddr}, 2)
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}

	// Create a rogue node that allows all networks
	// It should see others, but will not be seen by others
	c3 := testConfigNet(t, 2)
	c3.CIDRsAllowed, _ = ParseCIDRs([]string{"127.0.0.0/8"})
	c3.BindPort = bindPort

	m3, err := Create(c3)
	require.NoError(t, err)
	defer func() {
		if err := m3.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()
	// The rogue can see others, but others cannot see it
	err = joinAndTestMemberShip(t, m3, []string{m1.config.Name + "/" + m1.config.BindAddr}, 3)
	// For the node itself, everything seems fine, it should see others
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}

	// m1 and m2 should not see newcomer however
	if len(m1.Members()) != 2 {
		t.Fatalf("m1 should have 2 nodes! %v", m1.Members())
	}
	if m1.estNumNodes() != 2 {
		t.Fatalf("m1 should have 2 est. nodes! %v", m1.estNumNodes())
	}

	if len(m2.Members()) != 2 {
		t.Fatalf("m2 should have 2 nodes! %v", m2.Members())
	}
	if m2.estNumNodes() != 2 {
		t.Fatalf("m2 should have 2 est. nodes! %v", m2.estNumNodes())
	}

	// Another rogue, this time with a config that denies itself
	// Create a rogue node that allows all networks
	// It should see others, but will not be seen by others
	c4 := testConfigNet(t, 2)
	c4.CIDRsAllowed, _ = ParseCIDRs([]string{"127.0.0.0/24", "127.0.1.0/24"})
	c4.BindPort = bindPort

	m4, err := Create(c4)
	require.NoError(t, err)
	defer func() {
		if err := m4.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	// This time, the node should not even see itself, so 2 expected nodes
	_ = joinAndTestMemberShip(t, m4, []string{m1.config.BindAddr, m2.config.BindAddr}, 2)
	// m1 and m2 should not see newcomer however
	if len(m1.Members()) != 2 {
		t.Fatalf("m1 should have 2 nodes! %v", m1.Members())
	}
	if m1.estNumNodes() != 2 {
		t.Fatalf("m1 should have 2 est. nodes! %v", m1.estNumNodes())
	}

	if len(m2.Members()) != 2 {
		t.Fatalf("m2 should have 2 nodes! %v", m2.Members())
	}
	if m2.estNumNodes() != 2 {
		t.Fatalf("m2 should have 2 est. nodes! %v", m2.estNumNodes())
	}
}

type CustomMergeDelegate struct {
	invoked bool
	t       *testing.T
}

func (c *CustomMergeDelegate) NotifyMerge(nodes []*Node) error {
	c.t.Logf("Cancel merge")
	c.invoked = true
	return fmt.Errorf("Custom merge canceled")
}

func TestMemberlist_Join_Cancel(t *testing.T) {
	c1 := testConfig(t)
	merge1 := &CustomMergeDelegate{t: t}
	c1.Merge = merge1

	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	// Create a second node
	c2 := testConfig(t)
	c2.BindPort = bindPort
	merge2 := &CustomMergeDelegate{t: t}
	c2.Merge = merge2

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	num, err := m2.Join([]string{m1.config.Name + "/" + m1.config.BindAddr})
	if num != 0 {
		t.Fatalf("unexpected 0: %d", num)
	}
	if !strings.Contains(err.Error(), "Custom merge canceled") {
		t.Fatalf("unexpected err: %s", err)
	}

	// Check the hosts
	if len(m2.Members()) != 1 {
		t.Fatalf("should have 1 nodes! %v", m2.Members())
	}
	if len(m1.Members()) != 1 {
		t.Fatalf("should have 1 nodes! %v", m1.Members())
	}

	// Check delegate invocation
	if !merge1.invoked {
		t.Fatalf("should invoke delegate")
	}
	if !merge2.invoked {
		t.Fatalf("should invoke delegate")
	}
}

type CustomAliveDelegate struct {
	Ignore string
	count  int

	t *testing.T
}

func (c *CustomAliveDelegate) NotifyAlive(peer *Node) error {
	c.count++
	if peer.Name == c.Ignore {
		return nil
	}
	c.t.Logf("Cancel alive")
	return fmt.Errorf("Custom alive canceled")
}

func TestMemberlist_Join_Cancel_Passive(t *testing.T) {
	c1 := testConfig(t)
	alive1 := &CustomAliveDelegate{
		Ignore: c1.Name,
		t:      t,
	}
	c1.Alive = alive1

	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	// Create a second node
	c2 := testConfig(t)
	c2.BindPort = bindPort
	alive2 := &CustomAliveDelegate{
		Ignore: c2.Name,
		t:      t,
	}
	c2.Alive = alive2

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	num, err := m2.Join([]string{m1.config.Name + "/" + m1.config.BindAddr})
	if num != 1 {
		t.Fatalf("unexpected 1: %d", num)
	}
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	// Check the hosts
	if len(m2.Members()) != 1 {
		t.Fatalf("should have 1 nodes! %v", m2.Members())
	}
	if len(m1.Members()) != 1 {
		t.Fatalf("should have 1 nodes! %v", m1.Members())
	}

	// Check delegate invocation
	if alive1.count == 0 {
		t.Fatalf("should invoke delegate: %d", alive1.count)
	}
	if alive2.count == 0 {
		t.Fatalf("should invoke delegate: %d", alive2.count)
	}
}

func TestMemberlist_Join_protocolVersions(t *testing.T) {
	c1 := testConfig(t)

	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	c2 := testConfig(t)
	c2.BindPort = bindPort

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	c3 := testConfig(t)
	c3.BindPort = bindPort
	c3.ProtocolVersion = ProtocolVersionMax

	m3, err := Create(c3)
	require.NoError(t, err)
	defer func() {
		if err := m3.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	_, err = m1.Join([]string{c2.Name + "/" + c2.BindAddr})
	require.NoError(t, err)

	yield()

	_, err = m1.Join([]string{c3.Name + "/" + c3.BindAddr})
	require.NoError(t, err)
}

func joinAndTestMemberShip(t *testing.T, self *Memberlist, membersToJoin []string, expectedMembers int) error {
	t.Helper()
	num, err := self.Join(membersToJoin)
	if err != nil {
		return err
	}
	if num != len(membersToJoin) {
		t.Fatalf("unexpected %d, was expecting %d to be joined", num, len(membersToJoin))
	}
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}
	// Check the hosts
	if len(self.Members()) != expectedMembers {
		t.Fatalf("should have 2 nodes! %v", self.Members())
	}
	if len(self.Members()) != expectedMembers {
		t.Fatalf("should have 2 nodes! %v", self.Members())
	}
	return nil
}

func TestMemberlist_Leave(t *testing.T) {
	newConfig := func() *Config {
		c := testConfig(t)
		c.GossipInterval = time.Millisecond
		return c
	}

	c1 := newConfig()

	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	// Create a second node
	c2 := newConfig()
	c2.BindPort = bindPort

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	err = joinAndTestMemberShip(t, m2, []string{m1.config.Name + "/" + m1.config.BindAddr}, 2)
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}

	// Leave
	err = m1.Leave(time.Second)
	require.NoError(t, err)

	// Wait for leave
	time.Sleep(10 * time.Millisecond)

	// m1 should think dead
	if len(m1.Members()) != 1 {
		t.Fatalf("should have 1 node")
	}

	if len(m2.Members()) != 1 {
		t.Fatalf("should have 1 node")
	}

	if m2.nodeMap[c1.Name].State != StateLeft {
		t.Fatalf("bad state")
	}
}

func TestMemberlist_JoinShutdown(t *testing.T) {
	newConfig := func() *Config {
		c := testConfig(t)
		c.ProbeInterval = time.Millisecond
		c.ProbeTimeout = 100 * time.Microsecond
		c.SuspicionMaxTimeoutMult = 1
		return c
	}

	c1 := newConfig()

	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	// Create a second node
	c2 := newConfig()
	c2.BindPort = bindPort

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	num, err := m2.Join([]string{m1.config.Name + "/" + m1.config.BindAddr})
	if num != 1 {
		t.Fatalf("unexpected 1: %d", num)
	}
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}

	// Check the hosts
	if len(m2.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}

	require.NoError(t, m1.Shutdown())

	waitForCondition(t, func() (bool, string) {
		n := len(m2.Members())
		return n == 1, fmt.Sprintf("expected 1 node, got %d", n)
	})
}

func TestMemberlist_delegateMeta(t *testing.T) {
	c1 := testConfig(t)
	c1.Delegate = &MockDelegate{meta: []byte("web")}

	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	c2 := testConfig(t)
	c2.BindPort = bindPort
	c2.Delegate = &MockDelegate{meta: []byte("lb")}

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	_, err = m1.Join([]string{c2.Name + "/" + c2.BindAddr})
	require.NoError(t, err)

	yield()

	var roles map[string]string

	// Check the roles of members of m1
	m1m := m1.Members()
	if len(m1m) != 2 {
		t.Fatalf("bad: %#v", m1m)
	}

	roles = make(map[string]string)
	for _, m := range m1m {
		roles[m.Name] = string(m.Meta)
	}

	if r := roles[c1.Name]; r != "web" {
		t.Fatalf("bad role for %s: %s", c1.Name, r)
	}

	if r := roles[c2.Name]; r != "lb" {
		t.Fatalf("bad role for %s: %s", c2.Name, r)
	}

	// Check the roles of members of m2
	m2m := m2.Members()
	if len(m2m) != 2 {
		t.Fatalf("bad: %#v", m2m)
	}

	roles = make(map[string]string)
	for _, m := range m2m {
		roles[m.Name] = string(m.Meta)
	}

	if r := roles[c1.Name]; r != "web" {
		t.Fatalf("bad role for %s: %s", c1.Name, r)
	}

	if r := roles[c2.Name]; r != "lb" {
		t.Fatalf("bad role for %s: %s", c2.Name, r)
	}
}

func TestMemberlist_delegateMeta_Update(t *testing.T) {
	c1 := testConfig(t)
	mock1 := &MockDelegate{meta: []byte("web")}
	c1.Delegate = mock1

	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	c2 := testConfig(t)
	c2.BindPort = bindPort
	mock2 := &MockDelegate{meta: []byte("lb")}
	c2.Delegate = mock2

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	_, err = m1.Join([]string{c2.Name + "/" + c2.BindAddr})
	require.NoError(t, err)

	yield()

	// Update the meta data roles
	mock1.setMeta([]byte("api"))
	mock2.setMeta([]byte("db"))

	err = m1.UpdateNode(0)
	require.NoError(t, err)
	err = m2.UpdateNode(0)
	require.NoError(t, err)

	yield()

	// Check the updates have propagated
	var roles map[string]string

	// Check the roles of members of m1
	m1m := m1.Members()
	if len(m1m) != 2 {
		t.Fatalf("bad: %#v", m1m)
	}

	roles = make(map[string]string)
	for _, m := range m1m {
		roles[m.Name] = string(m.Meta)
	}

	if r := roles[c1.Name]; r != "api" {
		t.Fatalf("bad role for %s: %s", c1.Name, r)
	}

	if r := roles[c2.Name]; r != "db" {
		t.Fatalf("bad role for %s: %s", c2.Name, r)
	}

	// Check the roles of members of m2
	m2m := m2.Members()
	if len(m2m) != 2 {
		t.Fatalf("bad: %#v", m2m)
	}

	roles = make(map[string]string)
	for _, m := range m2m {
		roles[m.Name] = string(m.Meta)
	}

	if r := roles[c1.Name]; r != "api" {
		t.Fatalf("bad role for %s: %s", c1.Name, r)
	}

	if r := roles[c2.Name]; r != "db" {
		t.Fatalf("bad role for %s: %s", c2.Name, r)
	}
}

func TestMemberlist_UserData(t *testing.T) {
	newConfig := func() (*Config, *MockDelegate) {
		d := &MockDelegate{}
		c := testConfig(t)
		// Set the gossip/pushpull intervals fast enough to get a reasonable test,
		// but slow enough to avoid "sendto: operation not permitted"
		c.GossipInterval = 100 * time.Millisecond
		c.PushPullInterval = 100 * time.Millisecond
		c.Delegate = d
		return c, d
	}

	c1, d1 := newConfig()
	d1.setState([]byte("something"))

	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	bcasts := make([][]byte, 256)
	for i := range bcasts {
		bcasts[i] = []byte(fmt.Sprintf("%d", i))
	}

	// Create a second node
	c2, d2 := newConfig()
	c2.BindPort = bindPort

	// Second delegate has things to send
	d2.setBroadcasts(bcasts)
	d2.setState([]byte("my state"))

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	num, err := m2.Join([]string{m1.config.Name + "/" + m1.config.BindAddr})
	if num != 1 {
		t.Fatalf("unexpected 1: %d", num)
	}
	require.NoError(t, err)

	// Check the hosts
	if m2.NumMembers() != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}

	// Wait for a little while
	iretry.Run(t, func(r *iretry.R) {
		msgs1 := d1.getMessages()

		// Ensure we got the messages. Ordering of messages is not guaranteed so just
		// check we got them both in either order.
		require.ElementsMatch(r, bcasts, msgs1)

		rs1 := d1.getRemoteState()
		rs2 := d2.getRemoteState()

		// Check the push/pull state
		if !reflect.DeepEqual(rs1, []byte("my state")) {
			r.Fatalf("bad state %s", rs1)
		}
		if !reflect.DeepEqual(rs2, []byte("something")) {
			r.Fatalf("bad state %s", rs2)
		}
	})
}

func TestMemberlist_SendTo(t *testing.T) {
	newConfig := func() (*Config, *MockDelegate, net.IP) {
		d := &MockDelegate{}
		c := testConfig(t)
		c.GossipInterval = time.Millisecond
		c.PushPullInterval = time.Millisecond
		c.Delegate = d
		return c, d, net.ParseIP(c.BindAddr)
	}

	c1, d1, _ := newConfig()

	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	c2, d2, addr2 := newConfig()
	c2.BindPort = bindPort

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	num, err := m2.Join([]string{m1.config.Name + "/" + m1.config.BindAddr})
	require.NoError(t, err)
	require.Equal(t, 1, num)

	// Check the hosts
	require.Equal(t, 2, m2.NumMembers(), "should have 2 nodes! %v", m2.Members())

	// Try to do a direct send
	m2Addr := &net.UDPAddr{
		IP:   addr2,
		Port: bindPort,
	}
	m2Address := Address{
		Addr: m2Addr.String(),
		Name: m2.config.Name,
	}
	if err := m1.SendToAddress(m2Address, []byte("ping")); err != nil {
		t.Fatalf("err: %v", err)
	}

	m1Addr := &net.UDPAddr{
		IP:   net.ParseIP(m1.config.BindAddr),
		Port: bindPort,
	}
	m1Address := Address{
		Addr: m1Addr.String(),
		Name: m1.config.Name,
	}
	if err := m2.SendToAddress(m1Address, []byte("pong")); err != nil {
		t.Fatalf("err: %v", err)
	}

	waitForCondition(t, func() (bool, string) {
		msgs := d1.getMessages()
		return len(msgs) == 1, fmt.Sprintf("expected 1 message, got %d", len(msgs))
	})

	msgs1 := d1.getMessages()
	if !reflect.DeepEqual(msgs1[0], []byte("pong")) {
		t.Fatalf("bad msg %v", msgs1[0])
	}

	waitForCondition(t, func() (bool, string) {
		msgs := d2.getMessages()
		return len(msgs) == 1, fmt.Sprintf("expected 1 message, got %d", len(msgs))
	})
	msgs2 := d2.getMessages()
	if !reflect.DeepEqual(msgs2[0], []byte("ping")) {
		t.Fatalf("bad msg %v", msgs2[0])
	}
}

func waitForCondition(t *testing.T, fn func() (bool, string)) {
	start := time.Now()

	var msg string
	for time.Since(start) < 20*time.Second {
		var done bool
		done, msg = fn()
		if done {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for condition: %v", msg)
}

func TestMemberlistProtocolVersion(t *testing.T) {
	c := testConfig(t)
	c.ProtocolVersion = ProtocolVersionMax

	m, err := Create(c)
	require.NoError(t, err)
	defer func() {
		if err := m.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	result := m.ProtocolVersion()
	if result != ProtocolVersionMax {
		t.Fatalf("bad: %d", result)
	}
}

func TestMemberlist_Join_DeadNode(t *testing.T) {
	c1 := testConfig(t)
	c1.TCPTimeout = 50 * time.Millisecond

	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	// Create a second "node", which is just a TCP listener that
	// does not ever respond. This is to test our deadlines
	addr2 := getBindAddr()
	list, err := net.Listen("tcp", net.JoinHostPort(addr2.String(), strconv.Itoa(bindPort)))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer func() {
		if err := list.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Ensure we don't hang forever
	timer := time.AfterFunc(100*time.Millisecond, func() {
		panic("should have timed out by now")
	})
	defer timer.Stop()

	num, err := m1.Join([]string{"fake/" + addr2.String()})
	if num != 0 {
		t.Fatalf("unexpected 0: %d", num)
	}
	if err == nil {
		t.Fatal("expect err")
	}
}

// Tests that nodes running different versions of the protocol can successfully
// discover each other and add themselves to their respective member lists.
func TestMemberlist_Join_Protocol_Compatibility(t *testing.T) {
	testProtocolVersionPair := func(t *testing.T, pv1 uint8, pv2 uint8) {
		t.Helper()

		c1 := testConfig(t)
		c1.ProtocolVersion = pv1

		m1, err := Create(c1)
		require.NoError(t, err)
		defer func() {
			if err := m1.Shutdown(); err != nil {
				t.Fatal(err)
			}
		}()

		bindPort := m1.config.BindPort

		c2 := testConfig(t)
		c2.BindPort = bindPort
		c2.ProtocolVersion = pv2

		m2, err := Create(c2)
		require.NoError(t, err)
		defer func() {
			if err := m2.Shutdown(); err != nil {
				t.Fatal(err)
			}
		}()

		num, err := m2.Join([]string{m1.config.Name + "/" + m1.config.BindAddr})
		require.NoError(t, err)
		require.Equal(t, 1, num)

		// Check the hosts
		if len(m2.Members()) != 2 {
			t.Fatalf("should have 2 nodes! %v", m2.Members())
		}

		// Check the hosts
		if len(m1.Members()) != 2 {
			t.Fatalf("should have 2 nodes! %v", m1.Members())
		}
	}

	t.Run("2,1", func(t *testing.T) {
		testProtocolVersionPair(t, 2, 1)
	})
	t.Run("2,3", func(t *testing.T) {
		testProtocolVersionPair(t, 2, 3)
	})
	t.Run("3,2", func(t *testing.T) {
		testProtocolVersionPair(t, 3, 2)
	})
	t.Run("3,1", func(t *testing.T) {
		testProtocolVersionPair(t, 3, 1)
	})
}

var (
	ipv6LoopbackAvailableOnce sync.Once
	ipv6LoopbackAvailable     bool
)

func isIPv6LoopbackAvailable(t *testing.T) bool {
	const ipv6LoopbackAddress = "::1"
	ipv6LoopbackAvailableOnce.Do(func() {
		ifaces, err := net.Interfaces()
		require.NoError(t, err)

		for _, iface := range ifaces {
			if iface.Flags&net.FlagLoopback == 0 {
				continue
			}
			addrs, err := iface.Addrs()
			require.NoError(t, err)

			for _, addr := range addrs {
				ipaddr := addr.(*net.IPNet)
				if ipaddr.IP.String() == ipv6LoopbackAddress {
					ipv6LoopbackAvailable = true
					return
				}
			}
		}
		ipv6LoopbackAvailable = false
		t.Logf("IPv6 loopback address %q not found, disabling tests that require it", ipv6LoopbackAddress)
	})

	return ipv6LoopbackAvailable
}

func TestMemberlist_Join_IPv6(t *testing.T) {
	if !isIPv6LoopbackAvailable(t) {
		t.SkipNow()
		return
	}
	// Since this binds to all interfaces we need to exclude other tests
	// from grabbing an interface.
	bindLock.Lock()
	defer bindLock.Unlock()

	c1 := DefaultLANConfig()
	c1.Name = "A"
	c1.BindAddr = "[::1]"
	c1.BindPort = 0 // choose free
	c1.Logger = log.New(os.Stderr, c1.Name, log.LstdFlags)

	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create a second node
	c2 := DefaultLANConfig()
	c2.Name = "B"
	c2.BindAddr = "[::1]"
	c2.BindPort = 0 // choose free
	c2.Logger = log.New(os.Stderr, c2.Name, log.LstdFlags)

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	num, err := m2.Join([]string{fmt.Sprintf("%s/%s:%d", m1.config.Name, m1.config.BindAddr, m1.config.BindPort)})
	require.NoError(t, err)
	require.Equal(t, 1, num)

	// Check the hosts
	if len(m2.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}

	if len(m1.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}
}

func reservePort(t *testing.T, ip net.IP, purpose string) int {
	for i := 0; i < 10; i++ {
		tcpAddr := &net.TCPAddr{IP: ip, Port: 0}
		tcpLn, err := net.ListenTCP("tcp", tcpAddr)
		if err != nil {
			if strings.Contains(err.Error(), "address already in use") {
				continue
			}
			t.Fatalf("unexpected error: %v", err)
		}

		port := tcpLn.Addr().(*net.TCPAddr).Port

		udpAddr := &net.UDPAddr{IP: ip, Port: port}
		udpLn, err := net.ListenUDP("udp", udpAddr)
		if err != nil {
			_ = tcpLn.Close()
			if strings.Contains(err.Error(), "address already in use") {
				continue
			}
			t.Fatalf("unexpected error: %v", err)
		}

		t.Logf("Using dynamic bind port %d for %s", port, purpose)
		_ = tcpLn.Close()
		_ = udpLn.Close()
		return port
	}

	t.Fatalf("could not find a free TCP+UDP port to listen on for %s", purpose)
	panic("IMPOSSIBLE")
}

func TestAdvertiseAddr(t *testing.T) {
	bindAddr := getBindAddr()
	advertiseAddr := getBindAddr()

	bindPort := reservePort(t, bindAddr, "BIND")
	advertisePort := reservePort(t, advertiseAddr, "ADVERTISE")

	c := DefaultLANConfig()
	c.BindAddr = bindAddr.String()
	c.BindPort = bindPort
	c.Name = c.BindAddr

	c.AdvertiseAddr = advertiseAddr.String()
	c.AdvertisePort = advertisePort

	m, err := Create(c)
	require.NoError(t, err)
	defer func() {
		if err := m.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	yield()

	members := m.Members()
	require.Equal(t, 1, len(members))

	require.Equal(t, advertiseAddr.String(), members[0].Addr.String())
	require.Equal(t, advertisePort, int(members[0].Port))
}

type MockConflict struct {
	existing *Node
	other    *Node
}

func (m *MockConflict) NotifyConflict(existing, other *Node) {
	m.existing = existing
	m.other = other
}

func TestMemberlist_conflictDelegate(t *testing.T) {
	c1 := testConfig(t)
	mock := &MockConflict{}
	c1.Conflict = mock

	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	// Ensure name conflict
	c2 := testConfig(t)
	c2.Name = c1.Name
	c2.BindPort = bindPort

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	num, err := m1.Join([]string{c2.Name + "/" + c2.BindAddr})
	require.NoError(t, err)
	require.Equal(t, 1, num)

	yield()

	// Ensure we were notified
	if mock.existing == nil || mock.other == nil {
		t.Fatalf("should get notified mock.existing=%v  VS mock.other=%v", mock.existing, mock.other)
	}
	if mock.existing.Name != mock.other.Name {
		t.Fatalf("bad: %v %v", mock.existing, mock.other)
	}
}

type MockPing struct {
	mu      sync.Mutex
	other   *Node
	rtt     time.Duration
	payload []byte
}

func (m *MockPing) NotifyPingComplete(other *Node, rtt time.Duration, payload []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.other = other
	m.rtt = rtt
	m.payload = payload
}

func (m *MockPing) getContents() (*Node, time.Duration, []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.other, m.rtt, m.payload
}

const DEFAULT_PAYLOAD = "whatever"

func (m *MockPing) AckPayload() []byte {
	return []byte(DEFAULT_PAYLOAD)
}

func TestMemberlist_PingDelegate(t *testing.T) {
	newConfig := func() *Config {
		c := testConfig(t)
		c.ProbeInterval = 100 * time.Millisecond
		c.Ping = &MockPing{}
		return c
	}

	c1 := newConfig()

	m1, err := Create(c1)
	require.NoError(t, err)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	bindPort := m1.config.BindPort

	// Create a second node
	c2 := newConfig()
	c2.BindPort = bindPort
	mock := c2.Ping.(*MockPing)

	m2, err := Create(c2)
	require.NoError(t, err)
	defer func() {
		if err := m2.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	num, err := m2.Join([]string{m1.config.Name + "/" + m1.config.BindAddr})
	require.NoError(t, err)
	require.Equal(t, 1, num)

	waitUntilSize(t, m1, 2)
	waitUntilSize(t, m2, 2)

	time.Sleep(2 * c1.ProbeInterval)

	require.NoError(t, m1.Shutdown())
	require.NoError(t, m2.Shutdown())

	mOther, mRTT, mPayload := mock.getContents()

	// Ensure we were notified
	if mOther == nil {
		t.Fatalf("should get notified")
	}

	if !reflect.DeepEqual(mOther, m1.LocalNode()) {
		t.Fatalf("not notified about the correct node; expected: %+v; actual: %+v",
			m2.LocalNode(), mOther)
	}

	if mRTT <= 0 {
		t.Fatalf("rtt should be greater than 0")
	}

	if !bytes.Equal(mPayload, []byte(DEFAULT_PAYLOAD)) {
		t.Fatalf("incorrect payload. expected: %v; actual: %v",
			[]byte(DEFAULT_PAYLOAD), mPayload)
	}
}

func waitUntilSize(t *testing.T, m *Memberlist, expected int) {
	t.Helper()
	retry(t, 15, 500*time.Millisecond, func(failf func(string, ...interface{})) {
		t.Helper()

		if m.NumMembers() != expected {
			failf("%s expected to have %d members but had: %v", m.config.Name, expected, m.Members())
		}
	})
}

func isPortFree(t *testing.T, addr string, port int) error {
	t.Helper()

	ip := net.ParseIP(addr)
	tcpAddr := &net.TCPAddr{IP: ip, Port: port}
	tcpLn, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return err
	}
	if err := tcpLn.Close(); err != nil {
		return err
	}

	udpAddr := &net.UDPAddr{IP: ip, Port: port}
	udpLn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}

	return udpLn.Close()
}

func waitUntilPortIsFree(t *testing.T, m *Memberlist) {
	t.Helper()

	// wait until we know for certain that m1 is dead dead
	addr := m.config.BindAddr
	port := m.config.BindPort

	retry(t, 15, 250*time.Millisecond, func(failf func(string, ...interface{})) {
		t.Helper()

		if err := isPortFree(t, addr, port); err != nil {
			failf("%s port is not yet free", m.config.Name)
		}
	})
}

// This test should follow the recommended upgrade guide:
// https://www.consul.io/docs/agent/encryption.html#configuring-gossip-encryption-on-an-existing-cluster
//
// We will use two nodes for this: m0 and m1
//
// 0. Start with nodes without encryption.
// 1. Set an encryption key and set GossipVerifyIncoming=false and GossipVerifyOutgoing=false to all nodes.
// 2. Change GossipVerifyOutgoing=true to all nodes.
// 3. Change GossipVerifyIncoming=true to all nodes.
func TestMemberlist_EncryptedGossipTransition(t *testing.T) {
	// ensure these all get the same general set of customizations
	pretty := make(map[string]string) // addr->shortName
	newConfig := func(shortName string, addr string) *Config {
		t.Helper()

		conf := DefaultLANConfig()
		if addr == "" {
			addr = getBindAddr().String()
		}
		conf.Name = addr
		// conf.Name = shortName
		conf.BindAddr = addr
		conf.BindPort = 0
		// Set the gossip interval fast enough to get a reasonable test,
		// but slow enough to avoid "sendto: operation not permitted"
		conf.GossipInterval = 100 * time.Millisecond
		conf.Logger = log.New(os.Stderr, shortName, log.LstdFlags)

		pretty[conf.Name] = shortName
		return conf
	}

	var bindPort int
	createOK := func(conf *Config) *Memberlist {
		t.Helper()

		if bindPort > 0 {
			conf.BindPort = bindPort
		}
		m, err := Create(conf)
		require.NoError(t, err)

		if bindPort == 0 {
			bindPort = m.config.BindPort
		}
		return m
	}

	joinOK := func(src, dst *Memberlist, numNodes int) {
		t.Helper()

		srcName, dstName := pretty[src.config.Name], pretty[dst.config.Name]
		t.Logf("Node %s[%s] joining node %s[%s]", srcName, src.config.Name, dstName, dst.config.Name)

		num, err := src.Join([]string{dst.config.Name + "/" + dst.config.BindAddr})
		require.NoError(t, err)
		require.Equal(t, 1, num)

		waitUntilSize(t, src, numNodes)
		waitUntilSize(t, dst, numNodes)

		// Check the hosts
		require.Equal(t, numNodes, len(src.Members()), "nodes: %v", src.Members())
		require.Equal(t, numNodes, src.estNumNodes(), "nodes: %v", src.Members())
		require.Equal(t, numNodes, len(dst.Members()), "nodes: %v", dst.Members())
		require.Equal(t, numNodes, dst.estNumNodes(), "nodes: %v", dst.Members())
	}

	leaveOK := func(m *Memberlist, why string) {
		t.Helper()

		name := pretty[m.config.Name]
		t.Logf("Node %s[%s] is leaving %s", name, m.config.Name, why)
		err := m.Leave(time.Second)
		require.NoError(t, err)
	}

	shutdownOK := func(m *Memberlist, why string) {
		t.Helper()

		name := pretty[m.config.Name]
		t.Logf("Node %s[%s] is shutting down %s", name, m.config.Name, why)
		err := m.Shutdown()
		require.NoError(t, err)

		// Double check that it genuinely shutdown.
		waitUntilPortIsFree(t, m)
	}

	leaveAndShutdown := func(leaver, bystander *Memberlist, why string) {
		t.Helper()

		leaveOK(leaver, why)
		waitUntilSize(t, bystander, 1)
		shutdownOK(leaver, why)
		waitUntilSize(t, bystander, 1)
	}

	// ==== STEP 0 ====

	// Create a first cluster of 2 nodes with no gossip encryption settings.
	conf0 := newConfig("m0", "")
	m0 := createOK(conf0)
	defer func() {
		if err := m0.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	conf1 := newConfig("m1", "")
	m1 := createOK(conf1)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	joinOK(m1, m0, 2)

	t.Logf("==== STEP 0 complete: two node unencrypted cluster ====")

	// ==== STEP 1 ====

	// Take down m0, upgrade to first stage of gossip transition settings.
	leaveAndShutdown(m0, m1, "to upgrade gossip to first stage")

	// Resurrect the first node with the first stage of gossip transition settings.
	conf0 = newConfig("m0", m0.config.BindAddr)
	conf0.SecretKey = []byte("Hi16ZXu2lNCRVwtr20khAg==")
	conf0.GossipVerifyIncoming = false
	conf0.GossipVerifyOutgoing = false
	m0 = createOK(conf0)
	defer func() {
		if err := m0.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	// Join the second node. m1 has no encryption while m0 has encryption configured and
	// can receive encrypted gossip, but will not encrypt outgoing gossip.
	joinOK(m0, m1, 2)

	leaveAndShutdown(m1, m0, "to upgrade gossip to first stage")

	// Resurrect the second node with the first stage of gossip transition settings.
	conf1 = newConfig("m1", m1.config.BindAddr)
	conf1.SecretKey = []byte("Hi16ZXu2lNCRVwtr20khAg==")
	conf1.GossipVerifyIncoming = false
	conf1.GossipVerifyOutgoing = false
	m1 = createOK(conf1)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	// Join the first node. Both have encryption configured and can receive
	// encrypted gossip, but will not encrypt outgoing gossip.
	joinOK(m1, m0, 2)

	t.Logf("==== STEP 1 complete: two node encryption-aware cluster ====")

	// ==== STEP 2 ====

	// Take down m0, upgrade to second stage of gossip transition settings.
	leaveAndShutdown(m0, m1, "to upgrade gossip to second stage")

	// Resurrect the first node with the second stage of gossip transition settings.
	conf0 = newConfig("m0", m0.config.BindAddr)
	conf0.SecretKey = []byte("Hi16ZXu2lNCRVwtr20khAg==")
	conf0.GossipVerifyIncoming = false
	m0 = createOK(conf0)
	defer func() {
		if err := m0.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	// Join the second node. At this step, both nodes have encryption
	// configured but only m0 is sending encrypted gossip.
	joinOK(m0, m1, 2)

	leaveAndShutdown(m1, m0, "to upgrade gossip to second stage")

	// Resurrect the second node with the second stage of gossip transition settings.
	conf1 = newConfig("m1", m1.config.BindAddr)
	conf1.SecretKey = []byte("Hi16ZXu2lNCRVwtr20khAg==")
	conf1.GossipVerifyIncoming = false
	m1 = createOK(conf1)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	// Join the first node. Both have encryption configured and can receive
	// encrypted gossip, and encrypt outgoing gossip, but aren't forcing
	// incoming gossip is encrypted.
	joinOK(m1, m0, 2)

	t.Logf("==== STEP 2 complete: two node encryption-aware cluster being encrypted ====")

	// ==== STEP 3 ====

	// Take down m0, upgrade to final stage of gossip transition settings.
	leaveAndShutdown(m0, m1, "to upgrade gossip to final stage")

	// Resurrect the first node with the final stage of gossip transition settings.
	conf0 = newConfig("m0", m0.config.BindAddr)
	conf0.SecretKey = []byte("Hi16ZXu2lNCRVwtr20khAg==")
	m0 = createOK(conf0)
	defer func() {
		if err := m0.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	// Join the second node. At this step, both nodes have encryption
	// configured and are sending it, bu tonly m0 is verifying inbound gossip
	// is encrypted.
	joinOK(m0, m1, 2)

	leaveAndShutdown(m1, m0, "to upgrade gossip to final stage")

	// Resurrect the second node with the final stage of gossip transition settings.
	conf1 = newConfig("m1", m1.config.BindAddr)
	conf1.SecretKey = []byte("Hi16ZXu2lNCRVwtr20khAg==")
	m1 = createOK(conf1)
	defer func() {
		if err := m1.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}()

	// Join the first node. Both have encryption configured and fully in
	// enforcement.
	joinOK(m1, m0, 2)

	t.Logf("==== STEP 3 complete: two node encrypted cluster locked down ====")
}

// Consul bug, rapid restart (before failure detection),
// with an updated meta data. Should be at incarnation 1 for
// both.
//
// This test is uncommented because it requires that either we
// can rebind the socket (SO_REUSEPORT) which Go does not allow,
// OR we must disable the address conflict checking in memberlist.
// I just comment out that code to test this case.
//
//func TestMemberlist_Restart_delegateMeta_Update(t *testing.T) {
//    c1 := testConfig()
//    c2 := testConfig()
//    mock1 := &MockDelegate{meta: []byte("web")}
//    mock2 := &MockDelegate{meta: []byte("lb")}
//    c1.Delegate = mock1
//    c2.Delegate = mock2

//    m1, err := Create(c1)
//    if err != nil {
//        t.Fatalf("err: %s", err)
//    }
//    defer m1.Shutdown()

//    m2, err := Create(c2)
//    if err != nil {
//        t.Fatalf("err: %s", err)
//    }
//    defer m2.Shutdown()

//    _, err = m1.Join([]string{c2.BindAddr})
//    if err != nil {
//        t.Fatalf("err: %s", err)
//    }

//    yield()

//    // Recreate m1 with updated meta
//    m1.Shutdown()
//    c3 := testConfig()
//    c3.Name = c1.Name
//    c3.Delegate = mock1
//    c3.GossipInterval = time.Millisecond
//    mock1.meta = []byte("api")

//    m1, err = Create(c3)
//    if err != nil {
//        t.Fatalf("err: %s", err)
//    }
//    defer m1.Shutdown()

//    _, err = m1.Join([]string{c2.BindAddr})
//    if err != nil {
//        t.Fatalf("err: %s", err)
//    }

//    yield()
//    yield()

//    // Check the updates have propagated
//    var roles map[string]string

//    // Check the roles of members of m1
//    m1m := m1.Members()
//    if len(m1m) != 2 {
//        t.Fatalf("bad: %#v", m1m)
//    }

//    roles = make(map[string]string)
//    for _, m := range m1m {
//        roles[m.Name] = string(m.Meta)
//    }

//    if r := roles[c1.Name]; r != "api" {
//        t.Fatalf("bad role for %s: %s", c1.Name, r)
//    }

//    if r := roles[c2.Name]; r != "lb" {
//        t.Fatalf("bad role for %s: %s", c2.Name, r)
//    }

//    // Check the roles of members of m2
//    m2m := m2.Members()
//    if len(m2m) != 2 {
//        t.Fatalf("bad: %#v", m2m)
//    }

//    roles = make(map[string]string)
//    for _, m := range m2m {
//        roles[m.Name] = string(m.Meta)
//    }

//    if r := roles[c1.Name]; r != "api" {
//        t.Fatalf("bad role for %s: %s", c1.Name, r)
//    }

//    if r := roles[c2.Name]; r != "lb" {
//        t.Fatalf("bad role for %s: %s", c2.Name, r)
//    }
//}

func runStep(t *testing.T, name string, fn func(t *testing.T)) {
	t.Helper()
	if !t.Run(name, fn) {
		t.FailNow()
	}
}
