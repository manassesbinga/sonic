package runtime

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	defaultBucket = []byte("sonic")
)

// KVStore is a persistent, thread-safe key-value store for sharing state between workers.
// Workers in any language (JS, WASM, native) can read/write to this store.
// Backed by bbolt (embedded, zero external dependencies).
type KVStore struct {
	mu       sync.RWMutex
	db       *bolt.DB
	tempPath string // path to temp file (for cleanup in NewInMemoryKVStore)
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

	// Ensure the parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("failed to create KV store directory: %w", err)
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

// NewInMemoryKVStore creates a temporary KVStore backed by a temp file (for testing).
func NewInMemoryKVStore() (*KVStore, error) {
	tmpFile, err := os.CreateTemp("", "sonic-kv-*.db")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file for KV store: %w", err)
	}
	tmpFile.Close()
	dbPath := tmpFile.Name()

	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open in-memory KV store: %w", err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(defaultBucket)
		return err
	})
	if err != nil {
		db.Close()
		_ = os.Remove(dbPath)
		return nil, fmt.Errorf("failed to create bucket in in-memory KV store: %w", err)
	}
	return &KVStore{db: db, tempPath: dbPath}, nil
}

// encodeValueWithTTL encodes value + expiration timestamp (0 = no expiration)
func encodeValueWithTTL(value []byte, ttl time.Duration) []byte {
	var exp int64
	if ttl > 0 {
		exp = time.Now().Add(ttl).UnixNano()
	}
	// 8 bytes for timestamp + value
	buf := make([]byte, 8+len(value))
	binary.BigEndian.PutUint64(buf, uint64(exp))
	copy(buf[8:], value)
	return buf
}

// decodeValueWithTTL decodes and checks expiration.
// Returns a COPY of the value data (bbolt data is only valid inside the transaction).
func decodeValueWithTTL(data []byte) ([]byte, bool) {
	if len(data) < 8 {
		return nil, false
	}
	exp := int64(binary.BigEndian.Uint64(data[:8]))
	if exp > 0 && time.Now().UnixNano() > exp {
		return nil, false
	}
	val := make([]byte, len(data)-8)
	copy(val, data[8:])
	return val, true
}

// Get retrieves a value by key. Returns nil if the key doesn't exist or is expired.
func (kv *KVStore) Get(key string) []byte {
	var val []byte
	kv.mu.RLock()
	defer kv.mu.RUnlock()
	if err := kv.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		if b != nil {
			data := b.Get([]byte(key))
			if data != nil {
				val, _ = decodeValueWithTTL(data)
			}
		}
		return nil
	}); err != nil {
	}
	return val
}

// GetClient retrieves a value by key for a specific client. Returns nil if expired.
func (kv *KVStore) GetClient(clientID, key string) []byte {
	var val []byte
	kv.mu.RLock()
	defer kv.mu.RUnlock()
	if err := kv.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		if b != nil {
			data := b.Get([]byte("client:" + clientID + ":" + key))
			if data != nil {
				val, _ = decodeValueWithTTL(data)
			}
		}
		return nil
	}); err != nil {
	}
	return val
}

// Set stores a value by key with optional TTL (0 = no expiration)
func (kv *KVStore) Set(key string, value []byte, ttl ...time.Duration) error {
	var t time.Duration
	if len(ttl) > 0 {
		t = ttl[0]
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()
	err := kv.db.Batch(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		return b.Put([]byte(key), encodeValueWithTTL(value, t))
	})
	return err
}

// SetClient stores a value by key for a specific client with optional TTL
func (kv *KVStore) SetClient(clientID, key string, value []byte, ttl ...time.Duration) error {
	var t time.Duration
	if len(ttl) > 0 {
		t = ttl[0]
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()
	err := kv.db.Batch(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		return b.Put([]byte("client:"+clientID+":"+key), encodeValueWithTTL(value, t))
	})
	return err
}

// Delete removes a key from the store.
func (kv *KVStore) Delete(key string) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	err := kv.db.Batch(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		return b.Delete([]byte(key))
	})
	return err
}

// DeleteClient removes a key from the store for a specific client.
func (kv *KVStore) DeleteClient(clientID, key string) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	err := kv.db.Batch(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		return b.Delete([]byte("client:" + clientID + ":" + key))
	})
	return err
}

// Exists checks if a key exists and is not expired.
func (kv *KVStore) Exists(key string) bool {
	var exists bool
	kv.mu.RLock()
	defer kv.mu.RUnlock()
	if err := kv.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		if b != nil {
			data := b.Get([]byte(key))
			if data != nil {
				_, exists = decodeValueWithTTL(data)
			}
		}
		return nil
	}); err != nil {
	}
	return exists
}

// ExistsClient checks if a key exists and is not expired for a specific client.
func (kv *KVStore) ExistsClient(clientID, key string) bool {
	var exists bool
	kv.mu.RLock()
	defer kv.mu.RUnlock()
	if err := kv.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		if b != nil {
			data := b.Get([]byte("client:"+clientID+":"+key))
			if data != nil {
				_, exists = decodeValueWithTTL(data)
			}
		}
		return nil
	}); err != nil {
	}
	return exists
}

// Clear removes all keys from the store.
func (kv *KVStore) Clear() error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	return kv.db.Update(func(tx *bolt.Tx) error {
		err := tx.DeleteBucket(defaultBucket)
		if err != nil && err != bolt.ErrBucketNotFound {
			return err
		}
		_, err = tx.CreateBucket(defaultBucket)
		return err
	})
}

// Keys returns all non-expired keys in the store.
func (kv *KVStore) Keys() []string {
	var keys []string
	kv.mu.RLock()
	defer kv.mu.RUnlock()
	if err := kv.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			if _, ok := decodeValueWithTTL(v); ok {
				keys = append(keys, string(k))
			}
		}
		return nil
	}); err != nil {
	}
	return keys
}

// KeysClient returns all non-expired keys in the store for a specific client.
func (kv *KVStore) KeysClient(clientID string) []string {
	var keys []string
	prefix := []byte("client:" + clientID + ":")
	kv.mu.RLock()
	defer kv.mu.RUnlock()
	if err := kv.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && len(k) >= len(prefix) && string(k[:len(prefix)]) == string(prefix); k, v = c.Next() {
			if _, ok := decodeValueWithTTL(v); ok {
				keys = append(keys, string(k[len(prefix):]))
			}
		}
		return nil
	}); err != nil {
	}
	return keys
}

// Cleanup removes all expired keys from the store.
func (kv *KVStore) Cleanup() error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	return kv.db.Batch(func(tx *bolt.Tx) error {
		b := tx.Bucket(defaultBucket)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		var keysToDelete [][]byte
		for k, v := c.First(); k != nil; k, v = c.Next() {
			if _, ok := decodeValueWithTTL(v); !ok {
				keysToDelete = append(keysToDelete, append([]byte{}, k...))
			}
		}
		for _, k := range keysToDelete {
			_ = b.Delete(k)
		}
		return nil
	})
}

// Backup creates a backup of the KV store to the given file path.
// The file path is restricted to the data directory to prevent path traversal.
func (kv *KVStore) Backup(filePath string) error {
	cleanPath, err := sanitizePath(filePath, kv.dataDir())
	if err != nil {
		return fmt.Errorf("kv backup path invalid: %w", err)
	}
	kv.mu.RLock()
	defer kv.mu.RUnlock()
	return kv.db.View(func(tx *bolt.Tx) error {
		return tx.CopyFile(cleanPath, 0600)
	})
}

// Restore replaces the current KV store with a backup file.
// Warning: This will overwrite all existing data!
// The file path is restricted to the data directory to prevent path traversal.
func (kv *KVStore) Restore(backupFilePath string) error {
	cleanPath, err := sanitizePath(backupFilePath, kv.dataDir())
	if err != nil {
		return fmt.Errorf("kv restore path invalid: %w", err)
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()

	// 1. Criar um caminho temporário de staging no mesmo diretório para validar a cópia
	dbPath := kv.db.Path()
	stagingPath := dbPath + ".tmp"

	// Remover qualquer resquício de staging anterior
	_ = os.Remove(stagingPath)

	// 2. Copiar o backup para o staging
	if err := copyFile(cleanPath, stagingPath); err != nil {
		return fmt.Errorf("failed to copy backup to staging: %w", err)
	}
	defer os.Remove(stagingPath) // Garante a limpeza do staging no final

	// 3. Abrir o arquivo de staging para validar a integridade estrutural do banco
	verifyDB, err := bolt.Open(stagingPath, 0600, &bolt.Options{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("backup file is not a valid database: %w", err)
	}
	verifyDB.Close()

	// 4. Fechar o banco atual
	if err := kv.db.Close(); err != nil {
		return fmt.Errorf("failed to close current database: %w", err)
	}

	// 5. Substituir o arquivo de banco ativo pelo arquivo de staging validado
	if err := os.Rename(stagingPath, dbPath); err != nil {
		// Se falhar a renomeação, tentamos reabrir o banco original como fallback de emergência
		reopenDB, reopenErr := bolt.Open(dbPath, 0600, nil)
		if reopenErr == nil {
			kv.db = reopenDB
		}
		return fmt.Errorf("failed to overwrite database file: %w", err)
	}

	// 6. Reabrir a conexão com o novo banco ativo
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		return fmt.Errorf("failed to reopen database: %w", err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(defaultBucket)
		return err
	})
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to create default bucket after restore: %w", err)
	}

	kv.db = db
	InvalidatePipelinesCache()
	return nil
}

// sanitizePath restricts file operations to the given base directory to prevent path traversal.
func sanitizePath(path, baseDir string) (string, error) {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("base dir resolution failed: %w", err)
	}
	clean := filepath.Clean(path)
	absPath, err := filepath.Abs(clean)
	if err != nil {
		return "", fmt.Errorf("path resolution failed: %w", err)
	}
	if !strings.HasPrefix(absPath, absBase) {
		return "", fmt.Errorf("path traversal denied: %s is outside %s", path, baseDir)
	}
	return clean, nil
}

// dataDir returns the parent directory of the current database file.
func (kv *KVStore) dataDir() string {
	return filepath.Dir(kv.db.Path())
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

// Close closes the KV store and releases resources.
func (kv *KVStore) Close() error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	err := kv.db.Close()
	if kv.tempPath != "" {
		_ = os.Remove(kv.tempPath)
	}
	return err
}
