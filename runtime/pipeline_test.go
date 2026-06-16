package runtime

import (
	"testing"
)

func TestPipelineCache(t *testing.T) {
	// Create an in-memory KV Store
	kv, err := NewInMemoryKVStore()
	if err != nil {
		t.Fatalf("failed to create in-memory KV store: %v", err)
	}
	defer kv.Close()

	// Ensure cache is invalidated before test starts
	InvalidatePipelinesCache()

	// 1. Initially should be empty
	pipes, err := GetPipelines(kv)
	if err != nil {
		t.Fatalf("failed to get pipelines: %v", err)
	}
	if len(pipes) != 0 {
		t.Errorf("expected 0 pipelines, got %d", len(pipes))
	}

	// 2. Save a pipeline
	pipe := &Pipeline{
		ID:      "test-pipe",
		Name:    "Test Pipeline",
		Pattern: "*",
		Enabled: true,
	}
	err = SavePipeline(kv, pipe)
	if err != nil {
		t.Fatalf("failed to save pipeline: %v", err)
	}

	// 3. GetPipelines should now return the saved pipeline
	pipes, err = GetPipelines(kv)
	if err != nil {
		t.Fatalf("failed to get pipelines after save: %v", err)
	}
	if len(pipes) != 1 || pipes[0].ID != "test-pipe" {
		t.Errorf("expected to retrieve saved pipeline, got %v", pipes)
	}

	// 4. Manually modify BoltDB without calling SavePipeline to check that cache is hit
	err = kv.Delete("_sys:pipeline:test-pipe")
	if err != nil {
		t.Fatalf("failed to manually delete pipeline from BoltDB: %v", err)
	}

	// Since we bypassed SavePipeline, cache should still be hit and return 1 pipeline!
	pipes, err = GetPipelines(kv)
	if err != nil {
		t.Fatalf("failed to get pipelines: %v", err)
	}
	if len(pipes) != 1 {
		t.Errorf("expected cache hit returning 1 pipeline, got %d", len(pipes))
	}

	// 5. Invalidate the cache, and it should read the deletion from BoltDB and return 0
	InvalidatePipelinesCache()
	pipes, err = GetPipelines(kv)
	if err != nil {
		t.Fatalf("failed to get pipelines after invalidation: %v", err)
	}
	if len(pipes) != 0 {
		t.Errorf("expected cache invalidation to read updated DB and return 0, got %d", len(pipes))
	}
}
