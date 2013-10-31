package memberlist

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

const (
	encryptionVersion = 0
	versionSize       = 1
	nonceSize         = 12
	tagSize           = 16
	maxPadOverhead    = 16
	blockSize         = aes.BlockSize
)

// pkcs7encode is used to pad a byte buffer to a specific block size using
// the PKCS7 algorithm. "Ignores" some bytes to compensate for IV
func pkcs7encode(buf *bytes.Buffer, ignore, blockSize int) {
	n := buf.Len() - ignore
	more := blockSize - (n % blockSize)
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
	n -= int(last)
	return buf[:n]
}

// encryptedLength is used to compute the buffer size needed
// for a message of given length
func encryptedLength(inp int) int {
	// Determine the padding size
	padding := blockSize - (inp % blockSize)

	// Sum the extra parts to get total size
	return versionSize + nonceSize + inp + padding + tagSize
}

// encryptPayload is used to encrypt a message with a given key.
// We make use of AES-128 in GCM mode. New byte buffer is the version,
// nonce, ciphertext and tag
func encryptPayload(key []byte, msg []byte, data []byte, dst *bytes.Buffer) error {
	// Grow the buffer to make room for everything
	offset := dst.Len()
	dst.Grow(encryptedLength(len(msg)))

	// Write the encryption version
	dst.WriteByte(byte(encryptionVersion))

	// Add a random nonce
	io.CopyN(dst, rand.Reader, nonceSize)
	afterNonce := dst.Len()

	// Copy the message
	io.Copy(dst, bytes.NewReader(msg))

	// Ensure we are correctly padded
	pkcs7encode(dst, offset+versionSize+nonceSize, aes.BlockSize)

	// Get the AES block cipher
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		return err
	}

	// Get the GCM cipher mode
	gcm, err := cipher.NewGCM(aesBlock)
	if err != nil {
		return err
	}

	// Encrypt message using GCM
	slice := dst.Bytes()[offset:]
	nonce := slice[versionSize : versionSize+nonceSize]
	src := slice[versionSize+nonceSize:]
	out := gcm.Seal(nil, nonce, src, data)

	// Truncate the plaintext, and write the cipher text
	dst.Truncate(afterNonce)
	dst.Write(out)
	return nil
}

// decryptPayload is used to decrypt a message with a given key,
// and verify it's contents. Any padding will be removed, and a
// slice to the plaintext is returned. Decryption is done IN PLACE!
func decryptPayload(key []byte, msg []byte, data []byte) ([]byte, error) {
	// Ensure the length is sane
	if len(msg) <= encryptedLength(0) {
		return nil, fmt.Errorf("Payload is too small to decrypt")
	}

	// Verify the version
	if msg[0] != encryptionVersion {
		return nil, fmt.Errorf("Unsupported encryption version %d", msg[0])
	}

	// Get the AES block cipher
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	// Get the GCM cipher mode
	gcm, err := cipher.NewGCM(aesBlock)
	if err != nil {
		return nil, err
	}

	// Decrypt the message
	nonce := msg[versionSize : versionSize+nonceSize]
	ciphertext := msg[versionSize+nonceSize:]
	plain, err := gcm.Open(nil, nonce, ciphertext, data)
	if err != nil {
		return nil, err
	}

	// Remove the PKCS7 padding
	return pkcs7decode(plain, aes.BlockSize), nil
}
