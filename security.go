package memberlist

import (
	"bytes"
	"code.google.com/p/go.crypto/pbkdf2"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"fmt"
	"io"
)

const (
	KeySalt    = "\xb1\x94\x18k$\x9cc\xb4++of\x8e\xcd\x8c\x84\xf7\xf6F:\xd6d\x1e\x15\x82Mj\xd5~M\xa5<"
	KeyRounds  = 2048
	KeyLength  = aes.BlockSize
	HMACLength = sha1.Size
)

// deriveKey is used to generat the encryption key we use from the secret
// that is provided. We use PBKDF2 to ensure the key is crypto worthy
func deriveKey(secret []byte) []byte {
	return pbkdf2.Key(secret, []byte(KeySalt), KeyRounds, KeyLength, sha1.New)
}

// pkcs7encode is used to pad a byte buffer to a specific block size using
// the PKCS7 algorithm. "Ignores" some bytes to compensate for IV
func pkcs7encode(buf *bytes.Buffer, ignore, blockSize int) {
	n := buf.Len() - ignore
	more := blockSize - (n % blockSize)
	if more == blockSize {
		return
	}
	for i := 0; i < more; i++ {
		buf.WriteByte(byte(more))
	}
}

// pkcs7decode is used to decode a buffer that has been padded
func pkcs7decode(buf []byte, blockSize int) []byte {
	if len(buf) == 0 {
		panic("Cannot decode a PKCS7 buffer of zero length")
	}
	n := len(buf)
	last := buf[n-1]
	n -= (int(last) % blockSize)
	return buf[:n]
}

// encryptPayload is used to encrypt a message with a given key.
// We make use of AES-128 in CBC mode. New byte buffer is the
// IV, and encrypted text, aligned to 16byte block size
func encryptPayload(key []byte, msg []byte) (*bytes.Buffer, error) {
	// Create a buffer
	buf := bytes.NewBuffer(nil)

	// Add a random IV
	io.CopyN(buf, rand.Reader, aes.BlockSize)

	// Copy the message
	io.Copy(buf, bytes.NewReader(msg))

	// Ensure we are correctly padded
	pkcs7encode(buf, aes.BlockSize, aes.BlockSize)

	// Get the AES block cipher
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	// Encrypt message using CBC
	slice := buf.Bytes()
	cbc := cipher.NewCBCEncrypter(aesBlock, slice[:aes.BlockSize])
	cbc.CryptBlocks(slice[aes.BlockSize:], slice[aes.BlockSize:])
	return buf, nil
}

// decryptPayload is used to decrypt a message with a given key.
// Uses same algorithm as encryptPayload. Any padding will be
// removed, and a new slice is returned. Decryption is done
// IN PLACE!
func decryptPayload(key []byte, msg []byte) ([]byte, error) {
	// Get the AES block cipher
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	// Decrypt using CBC mode
	cbc := cipher.NewCBCDecrypter(aesBlock, msg[:aes.BlockSize])
	cbc.CryptBlocks(msg[aes.BlockSize:], msg[aes.BlockSize:])

	// Remove any padding
	noPad := pkcs7decode(msg[aes.BlockSize:], aes.BlockSize)
	return noPad, err
}

// hmacPayload is used to append an HMAC-SHA1 to a payload
// after computing the value using a given key
func hmacPayload(key []byte, msg *bytes.Buffer) error {
	// Create the HMAC
	mac := hmac.New(sha1.New, key)

	// Feed in the data
	_, err := mac.Write(msg.Bytes())
	if err != nil {
		return err
	}

	// Compute the hmac
	hmacSum := mac.Sum(nil)

	// Append the hmac to the bytes
	_, err = msg.Write(hmacSum)
	if err != nil {
		return err
	}
	return nil
}

// hmacVerifyPayload is used to verify the HMAC of a payload.
// It uses the last HMACLength bytes as the provided HMAC, and computes
// the HMAC of the preceeding bytes.
func hmacVerifyPayload(key []byte, buf []byte) error {
	if len(buf) <= HMACLength {
		return fmt.Errorf("Buffer is too short for HMAC verification")
	}

	// Extract the hmac, and the message
	n := len(buf)
	providedHMAC := buf[n-HMACLength:]
	msg := buf[:n-HMACLength]

	// Compute the HMAC
	mac := hmac.New(sha1.New, key)
	_, err := mac.Write(msg)
	if err != nil {
		return err
	}
	hmacSum := mac.Sum(nil)

	// Verify equality
	if !hmac.Equal(providedHMAC, hmacSum) {
		return fmt.Errorf("HMAC verification failed")
	}
	return nil
}
