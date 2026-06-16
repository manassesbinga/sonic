package runtime

import (
	"testing"
	"time"
)

func TestNewInMemoryKVStore(t *testing.T) {
	kv, err := NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("NewInMemoryKVStore failed: %v", err)
	}
	defer kv.Close()
}

func TestKVSetGet(t *testing.T) {
	kv, err := NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("NewInMemoryKVStore failed: %v", err)
	}
	defer kv.Close()

	if err := kv.Set("key1", []byte("value1")); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	val := kv.Get("key1")
	if string(val) != "value1" {
		t.Errorf("expected 'value1', got '%s'", string(val))
	}
}

func TestKVGetNonExistent(t *testing.T) {
	kv, err := NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("NewInMemoryKVStore failed: %v", err)
	}
	defer kv.Close()

	val := kv.Get("nonexistent")
	if val != nil {
		t.Errorf("expected nil for nonexistent key, got %v", val)
	}
}

func TestKVDelete(t *testing.T) {
	kv, err := NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("NewInMemoryKVStore failed: %v", err)
	}
	defer kv.Close()

	kv.Set("key1", []byte("value1"))
	if err := kv.Delete("key1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	val := kv.Get("key1")
	if val != nil {
		t.Errorf("expected nil after delete, got %v", val)
	}
}

func TestKVExists(t *testing.T) {
	kv, err := NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("NewInMemoryKVStore failed: %v", err)
	}
	defer kv.Close()

	if kv.Exists("key1") {
		t.Error("expected false for nonexistent key")
	}

	kv.Set("key1", []byte("value1"))
	if !kv.Exists("key1") {
		t.Error("expected true after set")
	}
}

func TestKVTTL(t *testing.T) {
	kv, err := NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("NewInMemoryKVStore failed: %v", err)
	}
	defer kv.Close()

	kv.Set("key1", []byte("value1"), 100*time.Millisecond)

	val := kv.Get("key1")
	if string(val) != "value1" {
		t.Errorf("expected 'value1' before expiry, got '%s'", string(val))
	}

	time.Sleep(150 * time.Millisecond)

	val = kv.Get("key1")
	if val != nil {
		t.Errorf("expected nil after TTL expiry, got %v", val)
	}
}

func TestKVClear(t *testing.T) {
	kv, err := NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("NewInMemoryKVStore failed: %v", err)
	}
	defer kv.Close()

	kv.Set("key1", []byte("value1"))
	kv.Set("key2", []byte("value2"))

	if err := kv.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	if kv.Exists("key1") || kv.Exists("key2") {
		t.Error("expected no keys after clear")
	}
}

func TestKVKeys(t *testing.T) {
	kv, err := NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("NewInMemoryKVStore failed: %v", err)
	}
	defer kv.Close()

	kv.Set("a", []byte("1"))
	kv.Set("b", []byte("2"))
	kv.Set("c", []byte("3"))

	keys := kv.Keys()
	if len(keys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(keys))
	}
}

func TestKVClientScoped(t *testing.T) {
	kv, err := NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("NewInMemoryKVStore failed: %v", err)
	}
	defer kv.Close()

	if err := kv.SetClient("client1", "key1", []byte("val1")); err != nil {
		t.Fatalf("SetClient failed: %v", err)
	}

	val := kv.GetClient("client1", "key1")
	if string(val) != "val1" {
		t.Errorf("expected 'val1', got '%s'", string(val))
	}

	// Other client should not see it
	val = kv.GetClient("client2", "key1")
	if val != nil {
		t.Errorf("expected nil for different client, got %v", val)
	}

	if !kv.ExistsClient("client1", "key1") {
		t.Error("expected ExistsClient to be true")
	}

	if err := kv.DeleteClient("client1", "key1"); err != nil {
		t.Fatalf("DeleteClient failed: %v", err)
	}

	if kv.ExistsClient("client1", "key1") {
		t.Error("expected false after DeleteClient")
	}
}

func TestKVCleanup(t *testing.T) {
	kv, err := NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("NewInMemoryKVStore failed: %v", err)
	}
	defer kv.Close()

	kv.Set("keep", []byte("stay"))
	kv.Set("expire", []byte("go"), 50*time.Millisecond)

	time.Sleep(100 * time.Millisecond)

	if err := kv.Cleanup(); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	if !kv.Exists("keep") {
		t.Error("expected 'keep' to still exist")
	}
	if kv.Exists("expire") {
		t.Error("expected 'expire' to be cleaned up")
	}
}
