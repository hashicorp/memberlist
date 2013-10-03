package memberlist

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/ugorji/go/codec"
	"net"
	"testing"
	"time"
)

func TestHandleCompoundPing(t *testing.T) {
	m := GetMemberlist(t)
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
	addr := &net.UDPAddr{IP: []byte{127, 0, 0, 1}, Port: m.config.UDPPort}
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
		if err := decode(in[1:], &ack); err != nil {
			t.Fatalf("unexpected err %s", err)
		}

		if ack.SeqNo != 42 {
			t.Fatalf("bad sequence no")
		}
	}
}

func TestHandlePing(t *testing.T) {
	m := GetMemberlist(t)
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
	addr := &net.UDPAddr{IP: []byte{127, 0, 0, 1}, Port: m.config.UDPPort}
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
	if err := decode(in[1:], &ack); err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	if ack.SeqNo != 42 {
		t.Fatalf("bad sequence no")
	}
}

func TestHandleIndirectPing(t *testing.T) {
	m := GetMemberlist(t)
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
	ind := indirectPingReq{SeqNo: 100, Target: []byte{127, 0, 0, 1}}
	buf, err := encode(indirectPingMsg, &ind)
	if err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	// Send
	addr := &net.UDPAddr{IP: []byte{127, 0, 0, 1}, Port: m.config.UDPPort}
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
	if err := decode(in[1:], &ack); err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	if ack.SeqNo != 100 {
		t.Fatalf("bad sequence no")
	}
}

func TestTCPPushPull(t *testing.T) {
	m := GetMemberlist(t)
	defer m.Shutdown()
	m.nodes = append(m.nodes, &nodeState{
		Node: Node{
			Name: "Test 0",
			Addr: []byte{127, 0, 0, 1},
		},
		Incarnation: 0,
		State:       stateSuspect,
		StateChange: time.Now().Add(-1 * time.Second),
	})

	addr := fmt.Sprintf("127.0.0.1:%d", m.config.TCPPort)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("unexpected err %s", err)
	}
	defer conn.Close()

	localNodes := make([]pushNodeState, 3)
	localNodes[0].Name = "Test 0"
	localNodes[0].Addr = []byte{127, 0, 0, 1}
	localNodes[0].Incarnation = 1
	localNodes[0].State = stateAlive
	localNodes[1].Name = "Test 1"
	localNodes[1].Addr = []byte{127, 0, 0, 1}
	localNodes[1].Incarnation = 1
	localNodes[1].State = stateAlive
	localNodes[2].Name = "Test 2"
	localNodes[2].Addr = []byte{127, 0, 0, 1}
	localNodes[2].Incarnation = 1
	localNodes[2].State = stateAlive

	// Send our node state
	header := pushPullHeader{Nodes: 3}
	hd := codec.MsgpackHandle{}
	enc := codec.NewEncoder(conn, &hd)

	// Send the push/pull indicator
	conn.Write([]byte{byte(pushPullMsg)})

	if err := enc.Encode(&header); err != nil {
		t.Fatalf("unexpected err %s", err)
	}
	for i := 0; i < header.Nodes; i++ {
		if err := enc.Encode(&localNodes[i]); err != nil {
			t.Fatalf("unexpected err %s", err)
		}
	}

	// Read the message type
	var msgType messageType
	if err := binary.Read(conn, binary.BigEndian, &msgType); err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	// Quit if not push/pull
	if msgType != pushPullMsg {
		t.Fatalf("bad message type")
	}

	msghd := codec.MsgpackHandle{}
	dec := codec.NewDecoder(conn, &msghd)
	if err := dec.Decode(&header); err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	// Allocate space for the transfer
	remoteNodes := make([]pushNodeState, header.Nodes)

	// Try to decode all the states
	for i := 0; i < header.Nodes; i++ {
		if err := dec.Decode(&remoteNodes[i]); err != nil {
			t.Fatalf("unexpected err %s", err)
		}
	}

	if len(remoteNodes) != 1 {
		t.Fatalf("bad response")
	}

	n := &remoteNodes[0]
	if n.Name != "Test 0" {
		t.Fatalf("bad name")
	}
	if bytes.Compare(n.Addr, []byte{127, 0, 0, 1}) != 0 {
		t.Fatal("bad addr")
	}
	if n.Incarnation != 0 {
		t.Fatal("bad incarnation")
	}
	if n.State != stateSuspect {
		t.Fatal("bad state")
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
	addr := &net.UDPAddr{IP: []byte{127, 0, 0, 1}, Port: m.config.UDPPort}
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
	if msgType != compoundMsg {
		t.Fatalf("bad response %v", in)
	}

	// get the parts
	trunc, parts, err := decodeCompoundMessage(in[1:])
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
	if err := decode(parts[0][1:], &ack); err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	if ack.SeqNo != 42 {
		t.Fatalf("bad sequence no")
	}

	var aliveout alive
	if err := decode(parts[1][1:], &aliveout); err != nil {
		t.Fatalf("unexpected err %s", err)
	}

	if aliveout.Node != "rand" || aliveout.Incarnation != 10 {
		t.Fatalf("bad mesg")
	}
}
