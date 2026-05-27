package runtime

import (
	"testing"
)

func generateTestWasmModule() []byte {
	return []byte{
		// WASM Magic + Version
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,

		// Type section: 3 types
		0x01, 0x10,
		0x03,
		0x60, 0x01, 0x7f, 0x01, 0x7f, // 0: (i32) -> i32   [malloc]
		0x60, 0x01, 0x7f, 0x00, // 1: (i32) -> ()    [free]
		0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7e, // 2: (i32,i32)-> i64 [process_packet]

		// Function section: 3 functions
		0x03, 0x04,
		0x03, 0x00, 0x01, 0x02,

		// Memory section: 1 page min, no max
		0x05, 0x03,
		0x01, 0x00, 0x01,

		// Export section: memory + 3 functions
		0x07, 0x2b,
		0x04,
		0x06, 0x6d, 0x65, 0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, // "memory" mem=0
		0x06, 0x6d, 0x61, 0x6c, 0x6c, 0x6f, 0x63, 0x00, 0x00, // "malloc" func=0
		0x04, 0x66, 0x72, 0x65, 0x65, 0x00, 0x01, // "free" func=1
		0x0e, 0x70, 0x72, 0x6f, 0x63, 0x65, 0x73, 0x73, 0x5f, 0x70, 0x61, 0x63, 0x6b, 0x65, 0x74, 0x00, 0x02, // "process_packet" func=2

		// Code section: 3 bodies
		0x0a, 0x16,
		0x03,
		0x04, 0x00, 0x41, 0x00, 0x0b, // malloc: return 0
		0x02, 0x00, 0x0b, // free: no-op
		0x0c, 0x00, 0x20, 0x00, 0xac, 0x20, 0x01, 0xac, 0x42, 0x20, 0x86, 0x84, 0x0b, // process_packet
	}
}

func TestWasmEngine_InvalidBytes(t *testing.T) {
	_, err := NewWasmEngineFromBytes([]byte{0, 0, 0, 0}, 100, 1, nil)
	if err == nil {
		t.Fatal("expected error for invalid wasm bytes, got nil")
	}
}

func TestWasmEngine_LoadAndType(t *testing.T) {
	engine, err := NewWasmEngineFromBytes(generateTestWasmModule(), 100, 1, nil)
	if err != nil {
		t.Fatalf("failed to create wasm engine: %v", err)
	}
	defer engine.Close()

	if engine.Type() != WorkerTypeWebAssembly {
		t.Fatalf("expected WorkerTypeWebAssembly, got %s", engine.Type())
	}
}

func TestWasmEngine_ProcessPacketBasic(t *testing.T) {
	engine, err := NewWasmEngineFromBytes(generateTestWasmModule(), 100, 1, nil)
	if err != nil {
		t.Fatalf("failed to create wasm engine: %v", err)
	}
	defer engine.Close()

	packet := &Packet{
		ID:         "test-123",
		Protocol:   ProtocolHTTP,
		Source:     "127.0.0.1:8080",
		Dest:       "example.com:80",
		IsInbound:  true,
		Data:       []byte("GET / HTTP/1.1"),
	}

	result, err := engine.ProcessPacket(packet)
	if err != nil {
		t.Fatalf("ProcessPacket failed: %v", err)
	}

	if result == nil {
		t.Fatal("expected result, got nil")
	}
}

func TestWasmEngine_Timeout(t *testing.T) {
	engine, err := NewWasmEngineFromBytes(generateTestWasmModule(), 1, 1, nil)
	if err != nil {
		t.Fatalf("failed to create wasm engine: %v", err)
	}
	defer engine.Close()

	packet := &Packet{
		ID:       "timeout-test",
		Protocol: ProtocolHTTP,
	}

	result, err := engine.ProcessPacket(packet)
	if err != nil {
		t.Fatalf("ProcessPacket should not timeout on trivial module: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
}

func TestWasmEngine_Close(t *testing.T) {
	engine, err := NewWasmEngineFromBytes(generateTestWasmModule(), 100, 2, nil)
	if err != nil {
		t.Fatalf("failed to create wasm engine: %v", err)
	}

	if err := engine.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestWasmEngine_InvalidWasmFile(t *testing.T) {
	_, err := NewWasmEngine("nonexistent.wasm", 100, 1, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}
