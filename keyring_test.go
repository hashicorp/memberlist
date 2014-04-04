package memberlist

import (
	"bytes"
	"testing"
)

func TestKeyring(t *testing.T) {
	key1 := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	key2 := []byte{15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0}

	keyring, err := NewKeyring(nil, nil)
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	if err := keyring.AddKey(key1); err == nil {
		t.Fatalf("Expected encryption disabled error")
	}

	keyring, err = NewKeyring(nil, key1)
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	if err := keyring.UseKey(key2); err == nil {
		t.Fatalf("Expected key not installed error")
	}

	if err := keyring.AddKey(key2); err != nil {
		t.Fatalf("err: %s", err)
	}

	keys := keyring.GetKeys()
	if len(keys) != 2 {
		t.Fatalf("Expected 2 keys but have %d", len(keys))
	}

	if !bytes.Equal(keys[0], key1) {
		t.Fatalf("Unexpected active key change")
	}

	if err := keyring.RemoveKey(key1); err == nil {
		t.Fatalf("Expected key active error")
	}

	if err := keyring.UseKey(key2); err != nil {
		t.Fatalf("err: %s", err)
	}

	keys = keyring.GetKeys()
	if !bytes.Equal(keys[0], key2) {
		t.Fatalf("Expected key change but key is unchanged")
	}

	if err := keyring.RemoveKey(key1); err != nil {
		t.Fatalf("err: %s", err)
	}

	keys = keyring.GetKeys()
	if len(keys) != 1 {
		t.Fatalf("Expected 1 key but have %d", len(keys))
	}
}
