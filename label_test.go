// Copyright IBM Corp. 2013, 2025
// SPDX-License-Identifier: MPL-2.0

package memberlist

import (
	"bytes"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddLabelHeaderToPacket(t *testing.T) {
	type testcase struct {
		buf          []byte
		label        string
		expectPacket []byte
		expectErr    string
	}

	run := func(t *testing.T, tc testcase) {
		got, err := AddLabelHeaderToPacket(tc.buf, tc.label)
		if tc.expectErr != "" {
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectErr)
		} else {
			require.NoError(t, err)
			require.Equal(t, tc.expectPacket, got)
		}
	}

	longLabel := strings.Repeat("a", 255)

	cases := map[string]testcase{
		"nil buf with no label": testcase{
			buf:          nil,
			label:        "",
			expectPacket: nil,
		},
		"nil buf with label": testcase{
			buf:          nil,
			label:        "foo",
			expectPacket: append([]byte{byte(hasLabelMsg), 3}, []byte("foo")...),
		},
		"message with label": testcase{
			buf:          []byte("something"),
			label:        "foo",
			expectPacket: append([]byte{byte(hasLabelMsg), 3}, []byte("foosomething")...),
		},
		"message with no label": testcase{
			buf:          []byte("something"),
			label:        "",
			expectPacket: []byte("something"),
		},
		"message with almost too long label": testcase{
			buf:          []byte("something"),
			label:        longLabel,
			expectPacket: append([]byte{byte(hasLabelMsg), 255}, []byte(longLabel+"something")...),
		},
		"label too long by one byte": testcase{
			buf:       []byte("something"),
			label:     longLabel + "x",
			expectErr: `label "` + longLabel + `x" is too long`,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			run(t, tc)
		})
	}
}

func TestRemoveLabelHeaderFromPacket(t *testing.T) {
	type testcase struct {
		buf          []byte
		expectLabel  string
		expectPacket []byte
		expectErr    string
	}

	run := func(t *testing.T, tc testcase) {
		gotBuf, gotLabel, err := RemoveLabelHeaderFromPacket(tc.buf)
		if tc.expectErr != "" {
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectErr)
		} else {
			require.NoError(t, err)
			require.Equal(t, tc.expectPacket, gotBuf)
			require.Equal(t, tc.expectLabel, gotLabel)
		}
	}

	cases := map[string]testcase{
		"empty buf": testcase{
			buf:          []byte{},
			expectLabel:  "",
			expectPacket: []byte{},
		},
		"ping with no label": testcase{
			buf:          buildBuffer(t, pingMsg, "blah"),
			expectLabel:  "",
			expectPacket: buildBuffer(t, pingMsg, "blah"),
		},
		"error with no label": testcase{ // 2021-10: largest standard message type
			buf:          buildBuffer(t, errMsg, "blah"),
			expectLabel:  "",
			expectPacket: buildBuffer(t, errMsg, "blah"),
		},
		"v1 encrypt with no label": testcase{ // 2021-10: highest encryption version
			buf:          buildBuffer(t, maxEncryptionVersion, "blah"),
			expectLabel:  "",
			expectPacket: buildBuffer(t, maxEncryptionVersion, "blah"),
		},
		"buf too small for label": testcase{
			buf:       buildBuffer(t, hasLabelMsg, "x"),
			expectErr: `cannot decode label; packet has been truncated`,
		},
		"buf too small for label size": testcase{
			buf:       buildBuffer(t, hasLabelMsg),
			expectErr: `cannot decode label; packet has been truncated`,
		},
		"label empty": testcase{
			buf:       buildBuffer(t, hasLabelMsg, 0, "x"),
			expectErr: `label header cannot be empty when present`,
		},
		"label truncated": testcase{
			buf:       buildBuffer(t, hasLabelMsg, 2, "x"),
			expectErr: `cannot decode label; packet has been truncated`,
		},
		"ping with label": testcase{
			buf:          buildBuffer(t, hasLabelMsg, 3, "abc", pingMsg, "blah"),
			expectLabel:  "abc",
			expectPacket: buildBuffer(t, pingMsg, "blah"),
		},
		"error with label": testcase{ // 2021-10: largest standard message type
			buf:          buildBuffer(t, hasLabelMsg, 3, "abc", errMsg, "blah"),
			expectLabel:  "abc",
			expectPacket: buildBuffer(t, errMsg, "blah"),
		},
		"v1 encrypt with label": testcase{ // 2021-10: highest encryption version
			buf:          buildBuffer(t, hasLabelMsg, 3, "abc", maxEncryptionVersion, "blah"),
			expectLabel:  "abc",
			expectPacket: buildBuffer(t, maxEncryptionVersion, "blah"),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			run(t, tc)
		})
	}
}

func TestAddLabelHeaderToStream(t *testing.T) {
	type testcase struct {
		label      string
		expectData []byte
		expectErr  string
	}

	suffixData := "EXTRA DATA"

	run := func(t *testing.T, tc testcase) {
		server, client := net.Pipe()
		defer func() {
			_ = server.Close()
		}()
		defer func() {
			_ = client.Close()
		}()

		var (
			dataCh = make(chan []byte, 1)
			errCh  = make(chan error, 1)
		)
		go func() {
			var buf bytes.Buffer
			_, err := io.Copy(&buf, server)
			if err != nil {
				errCh <- err
			}
			dataCh <- buf.Bytes()
		}()

		err := AddLabelHeaderToStream(client, tc.label)
		if tc.expectErr != "" {
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectErr)
			return
		}
		require.NoError(t, err)

		_, _ = client.Write([]byte(suffixData))
		_ = client.Close()

		expect := make([]byte, 0, len(suffixData)+len(tc.expectData))
		expect = append(expect, tc.expectData...)
		expect = append(expect, suffixData...)

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case got := <-dataCh:
			require.Equal(t, expect, got)
		}
	}

	longLabel := strings.Repeat("a", 255)

	cases := map[string]testcase{
		"no label": testcase{
			label:      "",
			expectData: nil,
		},
		"with label": testcase{
			label:      "foo",
			expectData: buildBuffer(t, hasLabelMsg, 3, "foo"),
		},
		"almost too long label": testcase{
			label:      longLabel,
			expectData: buildBuffer(t, hasLabelMsg, 255, longLabel),
		},
		"label too long by one byte": testcase{
			label:     longLabel + "x",
			expectErr: `label "` + longLabel + `x" is too long`,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			run(t, tc)
		})
	}
}

func TestRemoveLabelHeaderFromStream(t *testing.T) {
	type testcase struct {
		buf         []byte
		expectLabel string
		expectData  []byte
		expectErr   string
	}

	run := func(t *testing.T, tc testcase) {
		server, client := net.Pipe()
		defer func() {
			_ = server.Close()
		}()
		defer func() {
			_ = client.Close()
		}()

		var (
			errCh = make(chan error, 1)
		)
		go func() {
			_, err := server.Write(tc.buf)
			if err != nil {
				errCh <- err
			}
			_ = server.Close()
		}()

		newConn, gotLabel, err := RemoveLabelHeaderFromStream(client)
		if tc.expectErr != "" {
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectErr)
			return
		}
		require.NoError(t, err)

		gotBuf, err := io.ReadAll(newConn)
		require.NoError(t, err)

		require.Equal(t, tc.expectData, gotBuf)
		require.Equal(t, tc.expectLabel, gotLabel)
	}

	cases := map[string]testcase{
		"empty buf": testcase{
			buf:         []byte{},
			expectLabel: "",
			expectData:  []byte{},
		},
		"ping with no label": testcase{
			buf:         buildBuffer(t, pingMsg, "blah"),
			expectLabel: "",
			expectData:  buildBuffer(t, pingMsg, "blah"),
		},
		"error with no label": testcase{ // 2021-10: largest standard message type
			buf:         buildBuffer(t, errMsg, "blah"),
			expectLabel: "",
			expectData:  buildBuffer(t, errMsg, "blah"),
		},
		"v1 encrypt with no label": testcase{ // 2021-10: highest encryption version
			buf:         buildBuffer(t, maxEncryptionVersion, "blah"),
			expectLabel: "",
			expectData:  buildBuffer(t, maxEncryptionVersion, "blah"),
		},
		"buf too small for label": testcase{
			buf:       buildBuffer(t, hasLabelMsg, "x"),
			expectErr: `cannot decode label; stream has been truncated`,
		},
		"buf too small for label size": testcase{
			buf:       buildBuffer(t, hasLabelMsg),
			expectErr: `cannot decode label; stream has been truncated`,
		},
		"label empty": testcase{
			buf:       buildBuffer(t, hasLabelMsg, 0, "x"),
			expectErr: `label header cannot be empty when present`,
		},
		"label truncated": testcase{
			buf:       buildBuffer(t, hasLabelMsg, 2, "x"),
			expectErr: `cannot decode label; stream has been truncated`,
		},
		"ping with label": testcase{
			buf:         buildBuffer(t, hasLabelMsg, 3, "abc", pingMsg, "blah"),
			expectLabel: "abc",
			expectData:  buildBuffer(t, pingMsg, "blah"),
		},
		"error with label": testcase{ // 2021-10: largest standard message type
			buf:         buildBuffer(t, hasLabelMsg, 3, "abc", errMsg, "blah"),
			expectLabel: "abc",
			expectData:  buildBuffer(t, errMsg, "blah"),
		},
		"v1 encrypt with label": testcase{ // 2021-10: highest encryption version
			buf:         buildBuffer(t, hasLabelMsg, 3, "abc", maxEncryptionVersion, "blah"),
			expectLabel: "abc",
			expectData:  buildBuffer(t, maxEncryptionVersion, "blah"),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			run(t, tc)
		})
	}
}

func buildBuffer(t *testing.T, stuff ...interface{}) []byte {
	var buf bytes.Buffer
	for _, item := range stuff {
		switch x := item.(type) {
		case int:
			x2 := uint(x)
			if x2 > 255 {
				t.Fatalf("int is too big")
			}
			buf.WriteByte(byte(x2))
		case byte:
			buf.WriteByte(byte(x))
		case messageType:
			buf.WriteByte(byte(x))
		case encryptionVersion:
			buf.WriteByte(byte(x))
		case string:
			buf.Write([]byte(x))
		case []byte:
			buf.Write(x)
		default:
			t.Fatalf("unexpected type %T", item)
		}
	}
	return buf.Bytes()
}

func TestLabelOverhead(t *testing.T) {
	require.Equal(t, 0, labelOverhead(""))
	require.Equal(t, 3, labelOverhead("a"))
	require.Equal(t, 9, labelOverhead("abcdefg"))
}
