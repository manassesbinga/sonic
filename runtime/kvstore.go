package runtime

import (
	"fmt"
	"path/filepath"

	bolt "go.etcd.io/bbolt"
)

var (
	defaultBucket = []byte("sonic")
)

// KVStore is a persistent, thread-safe key-value store for sharing state between workers.
// Workers in any language (JS, WASM, native) can read/write to this store.
// Backed by bbolt (embedded, zero external dependencies).
type KVStore struct {
	db *bolt.DB
}

// NewKVStore creates a new KVStore instance with the given data directory.
// If dataDir is empty, uses "./data/sonic.kv".
func NewKVStore(dataDir ...string) (*KVStore, error) {
	var path string
	if len(dataDir) > 0 && dataDir[0] != "" {
		path = filepath.Join(dataDir[0], "sonic.kv")
	} else {
		path = filepath.Join(".", "data", "sonic.kv")
	}

	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open KV store: %w", err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(defaultBucket)
		return err
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create bucket: %w", err)
	}

	return &KVStore{db: db}, nil
}

// NewInMemoryKVStore creates a temporary, in-memory KVStore (for testing).
func NewInMemoryKVStore() *KVStore {
	db, _ := bolt.Open("", 0600, nil)
	_ = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(defaultBucket)
		return err
	})
	return &KVStore{db: db}
}

// Get retrieves a value by key. Returns nil if the key doesn't exist.
func (kv *KVStore) Get(key string) []byte {
	var val []byte
	_ = kv.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		if b != nil {
			val = b.Get([]byte(key))
		}
		return nil
	})
	return val
}

// Set stores a value by key.
func (kv *KVStore) Set(key string, value []byte) error {
	return kv.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		return b.Put([]byte(key), value)
	})
}

// Delete removes a key from the store.
func (kv *KVStore) Delete(key string) error {
	return kv.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		return b.Delete([]byte(key))
	})
}

// Exists checks if a key exists in the store.
func (kv *KVStore) Exists(key string) bool {
	var exists bool
	_ = kv.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		if b != nil {
			exists = b.Get([]byte(key)) != nil
		}
		return nil
	})
	return exists
}

// Clear removes all keys from the store.
func (kv *KVStore) Clear() error {
	return kv.db.Update(func(tx *bolt.Tx) error {
		err := tx.DeleteBucket(defaultBucket)
		if err != nil && err != bolt.ErrBucketNotFound {
			return err
		}
		_, err = tx.CreateBucket(defaultBucket)
		return err
	})
}

// Keys returns all keys in the store.
func (kv *KVStore) Keys() []string {
	var keys []string
	_ = kv.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			keys = append(keys, string(k))
		}
		return nil
	})
	return keys
}

// Close closes the KV store and releases resources.
func (kv *KVStore) Close() error {
	return kv.db.Close()
}
