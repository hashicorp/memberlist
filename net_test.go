package memberlist

import (
	"fmt"
	"net"
	"testing"
	"time"
)

func TestHandleCommand_badVersion(t *testing.T) {
	m := GetMemberlist(t)
	defer m.Shutdown()
	mAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d",
		m.config.BindAddr, m.config.UDPPort))
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	// Encode a ping
	ping := ping{SeqNo: 42}
	buf, err := encode(pingMsg, ping)
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	// Manually set the version too high
	bufBytes := buf.Bytes()
	bufBytes[1] = uint8(messageTypeVersions[pingMsg]) - 2

	// A fake addr to send a response back to (it'll never come back)
	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	defer ln.Close()

	// Handle it
	ln.WriteTo(bufBytes, mAddr)

	// Make sure we can never read
	ln.SetReadDeadline(time.Now().Add(50 * time.Millisecond))

	raw := make([]byte, 1500)
	_, _, err = ln.ReadFrom(raw)
	if err == nil {
		t.Fatal("should have error!")
	}
}

func TestHandleCompoundPing(t *testing.T) {
	m := GetMemberlist(t)
	m.config.EnableCompression = false
	defer m.Shutdown()

	var udp *net.UDPConn
	for port := 60000; port < 61000; port++ {
		udpAddr := fmt.Sprintf("127.0.0.1:%d", port)
		udpLn, err := net.ListenPacket("udp", udpAddr)
		if err == nil {
			udp = udpLn.(*net.UDPConn)
			break
		}
	}

	if udp == nil {
		t.Fatalf("no udp listener")
	}

	// Encode a ping
	ping := ping{SeqNo: 42}
	buf, err := encode(pingMsg, ping)
	if err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	// Make a compound message
	compound := makeCompoundMessage([][]byte{buf.Bytes(), buf.Bytes(), buf.Bytes()})

	// Send compound version
	addr := &net.UDPAddr{IP: net.ParseIP(m.config.BindAddr), Port: m.config.UDPPort}
	udp.WriteTo(compound.Bytes(), addr)

	// Wait for responses
	go func() {
		time.Sleep(time.Second)
		panic("timeout")
	}()

	for i := 0; i < 3; i++ {
		in := make([]byte, 1500)
		n, _, err := udp.ReadFrom(in)
		if err != nil {
			t.Fatalf("unexpected err %s", err)
		}
		in = in[0:n]

		msgType := messageType(in[0])
		if msgType != ackRespMsg {
			t.Fatalf("bad response %v", in)
		}

		var ack ackResp
		if err := decode(in[2:], &ack); err != nil {
			t.Fatalf("unexpected err %s", err)
		}

		if ack.SeqNo != 42 {
			t.Fatalf("bad sequence no")
		}
	}
}

func TestHandlePing(t *testing.T) {
	m := GetMemberlist(t)
	m.config.EnableCompression = false
	defer m.Shutdown()

	var udp *net.UDPConn
	for port := 60000; port < 61000; port++ {
		udpAddr := fmt.Sprintf("127.0.0.1:%d", port)
		udpLn, err := net.ListenPacket("udp", udpAddr)
		if err == nil {
			udp = udpLn.(*net.UDPConn)
			break
		}
	}

	if udp == nil {
		t.Fatalf("no udp listener")
	}

	// Encode a ping
	ping := ping{SeqNo: 42}
	buf, err := encode(pingMsg, ping)
	if err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	// Send
	addr := &net.UDPAddr{IP: net.ParseIP(m.config.BindAddr), Port: m.config.UDPPort}
	udp.WriteTo(buf.Bytes(), addr)

	// Wait for response
	go func() {
		time.Sleep(time.Second)
		panic("timeout")
	}()

	in := make([]byte, 1500)
	n, _, err := udp.ReadFrom(in)
	if err != nil {
		t.Fatalf("unexpected err %s", err)
	}
	in = in[0:n]

	msgType := messageType(in[0])
	if msgType != ackRespMsg {
		t.Fatalf("bad response %v", in)
	}

	var ack ackResp
	if err := decode(in[2:], &ack); err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	if ack.SeqNo != 42 {
		t.Fatalf("bad sequence no")
	}
}

func TestHandleIndirectPing(t *testing.T) {
	m := GetMemberlist(t)
	m.config.EnableCompression = false
	defer m.Shutdown()

	var udp *net.UDPConn
	for port := 60000; port < 61000; port++ {
		udpAddr := fmt.Sprintf("127.0.0.1:%d", port)
		udpLn, err := net.ListenPacket("udp", udpAddr)
		if err == nil {
			udp = udpLn.(*net.UDPConn)
			break
		}
	}

	if udp == nil {
		t.Fatalf("no udp listener")
	}

	// Encode an indirect ping
	ind := indirectPingReq{SeqNo: 100, Target: net.ParseIP(m.config.BindAddr)}
	buf, err := encode(indirectPingMsg, &ind)
	if err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	// Send
	addr := &net.UDPAddr{IP: net.ParseIP(m.config.BindAddr), Port: m.config.UDPPort}
	udp.WriteTo(buf.Bytes(), addr)

	// Wait for response
	go func() {
		time.Sleep(time.Second)
		panic("timeout")
	}()

	in := make([]byte, 1500)
	n, _, err := udp.ReadFrom(in)
	if err != nil {
		t.Fatalf("unexpected err %s", err)
	}
	in = in[0:n]

	msgType := messageType(in[0])
	if msgType != ackRespMsg {
		t.Fatalf("bad response %v", in)
	}

	var ack ackResp
	if err := decode(in[2:], &ack); err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	if ack.SeqNo != 100 {
		t.Fatalf("bad sequence no")
	}
}

func TestSendMsg_Piggyback(t *testing.T) {
	m := GetMemberlist(t)
	defer m.Shutdown()

	// Add a message to be broadcast
	a := alive{10, "rand", []byte{127, 0, 0, 255}, nil}
	m.encodeAndBroadcast("rand", aliveMsg, &a)

	var udp *net.UDPConn
	for port := 60000; port < 61000; port++ {
		udpAddr := fmt.Sprintf("127.0.0.1:%d", port)
		udpLn, err := net.ListenPacket("udp", udpAddr)
		if err == nil {
			udp = udpLn.(*net.UDPConn)
			break
		}
	}

	// Encode a ping
	ping := ping{SeqNo: 42}
	buf, err := encode(pingMsg, ping)
	if err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	// Send
	addr := &net.UDPAddr{IP: net.ParseIP(m.config.BindAddr), Port: m.config.UDPPort}
	udp.WriteTo(buf.Bytes(), addr)

	// Wait for response
	go func() {
		time.Sleep(time.Second)
		panic("timeout")
	}()

	in := make([]byte, 1500)
	n, _, err := udp.ReadFrom(in)
	if err != nil {
		t.Fatalf("unexpected err %s", err)
	}
	in = in[0:n]

	msgType := messageType(in[0])
	if msgType != compressMsg {
		t.Fatalf("bad response %v", in)
	}

	// Decompress first
	in, err = decompressPayload(in[1:])
	if err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	if messageVersion(in[1]) != messageTypeVersions[compoundMsg] {
		t.Fatalf("bad response %v", in)
	}

	// get the parts
	trunc, parts, err := decodeCompoundMessage(in[2:])
	if trunc != 0 {
		t.Fatalf("unexpected truncation")
	}
	if len(parts) != 2 {
		t.Fatalf("unexpected parts %v", parts)
	}
	if err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	var ack ackResp
	if err := decode(parts[0][2:], &ack); err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	if ack.SeqNo != 42 {
		t.Fatalf("bad sequence no")
	}

	var aliveout alive
	if err := decode(parts[1][2:], &aliveout); err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	if aliveout.Node != "rand" || aliveout.Incarnation != 10 {
		t.Fatalf("bad mesg")
	}
}
