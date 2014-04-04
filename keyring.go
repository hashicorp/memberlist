package memberlist

import (
	"bytes"
	"fmt"
	"sync"
)

type Keyring struct {
	// Indicates whether or not encryption is enabled
	enabled bool

	// The keyring lock gives us stronger consistency gurantees while performing
	// IO operations that alter or read from the keyring.
	keyringLock sync.Mutex

	// Keys stores the key data used during encryption and decryption. It is
	// ordered in such a way where the first key (index 0) is the primary key,
	// which is used for encrypting messages, and is the first key tried during
	// message decryption.
	keys [][]byte
}

// Init allocates substructures
func (k *Keyring) init() {
	k.keys = make([][]byte, 0)
}

func NewKeyring(keys [][]byte, primaryKey []byte) (*Keyring, error) {
	keyring := &Keyring{enabled: false}
	keyring.init()

	if len(keys) > 0 || len(primaryKey) > 0 {
		keyring.enabled = true
		if len(primaryKey) == 0 {
			return nil, fmt.Errorf("Empty primary key not allowed")
		}
		if err := keyring.AddKey(primaryKey); err != nil {
			return nil, err
		}
		for _, key := range keys {
			if err := keyring.AddKey(key); err != nil {
				return nil, err
			}
		}
	}

	return keyring, nil
}

func (k *Keyring) IsEnabled() bool {
	return k.enabled
}

// AddSecretKey will install a new key to the list of usable secret keys. Adding
// a key to the list will make it available for decrypting. If the key already
// exists in the key list, this function will just return noop.
func (k *Keyring) AddKey(key []byte) error {
	// Don't allow enabling encryption by adding a key
	if !k.enabled {
		return fmt.Errorf("encryption is not enabled")
	}

	if len(key) != 16 {
		return fmt.Errorf("key size must be 16 bytes")
	}

	// No-op if key is already installed
	for _, installedKey := range k.keys {
		if bytes.Equal(installedKey, key) {
			return nil
		}
	}

	keys := append(k.keys, key)
	primaryKey := k.GetPrimaryKey()
	if primaryKey == nil {
		primaryKey = key
	}
	k.setKeys(keys, primaryKey)
	return nil
}

// UseSecretKey changes the key used to encrypt messages. This is the only key
// used to encrypt messages, so peers should know this key before this method
// is invoked.
func (k *Keyring) UseKey(key []byte) error {
	for _, installedKey := range k.keys {
		if bytes.Equal(key, installedKey) {
			k.setKeys(k.keys, key)
			return nil
		}
	}
	return fmt.Errorf("Requested key is not in the keyring")
}

// RemoveSecretKey drops a key from the list of available keys. This will return
// an error if the key requested for removal is the currently active key.
func (k *Keyring) RemoveKey(key []byte) error {
	if bytes.Equal(key, k.keys[0]) {
		return fmt.Errorf("Removing the active key is not allowed")
	}
	for i, installedKey := range k.keys {
		if bytes.Equal(key, installedKey) {
			keys := append(k.keys[:i], k.keys[i+1:]...)
			k.setKeys(keys, k.keys[0])
		}
	}
	return nil
}

// setSecretKeys will take a list of keys, arrange them with primary key first,
// and set them into the config structure.
func (k *Keyring) setKeys(keys [][]byte, primaryKey []byte) {
	k.keyringLock.Lock()
	defer k.keyringLock.Unlock()

	installKeys := [][]byte{primaryKey}
	for _, key := range keys {
		if !bytes.Equal(key, primaryKey) {
			installKeys = append(installKeys, key)
		}
	}
	k.keys = installKeys
}

func (k *Keyring) GetKeys() [][]byte {
	k.keyringLock.Lock()
	defer k.keyringLock.Unlock()

	return k.keys
}

func (k *Keyring) GetPrimaryKey() (key []byte) {
	k.keyringLock.Lock()
	defer k.keyringLock.Unlock()

	if len(k.keys) > 0 {
		key = k.keys[0]
	}
	return
}
