package memberlist

import (
	"bytes"
	"crypto/aes"
	"reflect"
	"testing"
)

func TestDeriveKey(t *testing.T) {
	k1 := deriveKey([]byte("foobar"), []byte(keySalt))
	k2 := deriveKey([]byte("foobar"), []byte(keySalt))
	if bytes.Compare(k1, k2) != 0 {
		t.Fatalf("not equal")
	}

	k3 := deriveKey([]byte("test"), []byte(keySalt))
	if bytes.Compare(k1, k3) == 0 {
		t.Fatalf("should not be equal")
	}

	k4 := deriveKey([]byte("foobar"), []byte(hmacSalt))
	if bytes.Compare(k1, k4) == 0 {
		t.Fatalf("should not be equal")
	}

	if len(k1) != aes.BlockSize {
		t.Fatalf("bad len: %d", len(k1))
	}
	if len(k3) != aes.BlockSize {
		t.Fatalf("bad len: %d", len(k1))
	}
}

func TestPKCS7(t *testing.T) {
	for i := 1; i <= 255; i++ {
		// Make a buffer of size i
		buf := []byte{}
		for j := 0; j < i; j++ {
			buf = append(buf, byte(i))
		}

		// Copy to bytes buffer
		inp := bytes.NewBuffer(nil)
		inp.Write(buf)

		// Pad this out
		pkcs7encode(inp, 0, 16)

		// Unpad
		dec := pkcs7decode(inp.Bytes(), 16)

		// Ensure equivilence
		if !reflect.DeepEqual(buf, dec) {
			t.Fatalf("mismatch: %v %v", buf, dec)
		}
	}

}

func TestEncryptDecrypt(t *testing.T) {
	k1 := deriveKey([]byte("foobar"), []byte(keySalt))
	plaintext := []byte("this is a plain text message")

	buf, err := encryptPayload(k1, plaintext)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if (buf.Len()-1)%16 != 0 {
		t.Fatalf("output should be 16 byte aligned")
	}

	msg, err := decryptPayload(k1, buf.Bytes())
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if bytes.Compare(msg, plaintext) != 0 {
		t.Fatalf("encrypt/decrypt failed! %v", msg)
	}
}

func TestHMACVerify(t *testing.T) {
	k1 := deriveKey([]byte("foobar"), []byte(hmacSalt))
	plaintext := []byte("this is a plain text message")

	buf := bytes.NewBuffer(nil)
	buf.Write(plaintext)

	if err := hmacPayload(k1, buf); err != nil {
		t.Fatalf("err: %v", err)
	}

	if err := hmacVerifyPayload(k1, buf.Bytes()); err != nil {
		t.Fatalf("err: %v", err)
	}

	// Try different key!
	k1[0]++
	err := hmacVerifyPayload(k1, buf.Bytes())
	if err.Error() != "HMAC verification failed" {
		t.Fatalf("err: %v", err)
	}

	// Try a different payload
	k1[0]--
	buf.Bytes()[0]++
	err = hmacVerifyPayload(k1, buf.Bytes())
	if err.Error() != "HMAC verification failed" {
		t.Fatalf("err: %v", err)
	}
}
