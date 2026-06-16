package security

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCVEStreamScanner_LegitimatePayload(t *testing.T) {
	scanner := NewCVEStreamScanner(nil)
	payload := `function onTraffic(req) { log("request received"); return req; }`
	matched, sig := scanner.Scan([]byte(payload))
	if matched {
		t.Errorf("expected no match for legitimate payload, but matched signature: %s", sig)
	}
}

func TestCVEStreamScanner_JavaScriptExploits(t *testing.T) {
	scanner := NewCVEStreamScanner(nil)
	tests := []struct {
		name    string
		payload string
	}{
		{"Prototype Pollution", `const exploit = req.body.constructor.prototype.polluted = true;`},
		{"Insecure Eval Injection", `eval("console.log(" + req.body.input + ")");`},
		{"CVE-2021-23406 Exploit", `FindProxyForURL(url, host) { eval(host); }`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, sig := scanner.Scan([]byte(tt.payload))
			if !matched {
				t.Errorf("expected match for %s, but got none", tt.name)
			} else {
				t.Logf("matched signature: %s", sig)
			}
		})
	}
}

func TestCVEStreamScanner_PythonExploits(t *testing.T) {
	scanner := NewCVEStreamScanner(nil)
	tests := []struct {
		name    string
		payload string
	}{
		{"Unsafe Pickle Loads", `import pickle\npickle.loads(b"cos\nsystem\n(S'rm -rf /'\ntR.")`},
		{"Insecure Import OS RCE", `__import__('os').system('ls')`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, sig := scanner.Scan([]byte(tt.payload))
			if !matched {
				t.Errorf("expected match for %s, but got none", tt.name)
			} else {
				t.Logf("matched signature: %s", sig)
			}
		})
	}
}

func TestCVEStreamScanner_BashExploits(t *testing.T) {
	scanner := NewCVEStreamScanner(nil)
	tests := []struct {
		name    string
		payload string
	}{
		{"Shellshock", `() { :; }; echo "Vulnerable"`},
		{"Insecure Command Injection", `payload; rm -rf /etc/passwd`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, sig := scanner.Scan([]byte(tt.payload))
			if !matched {
				t.Errorf("expected match for %s, but got none", tt.name)
			} else {
				t.Logf("matched signature: %s", sig)
			}
		})
	}
}

func TestScanningReader_BlockingAndEvents(t *testing.T) {
	var events []SecurityEvent
	onEvent := func(event SecurityEvent, details map[string]interface{}) {
		events = append(events, event)
	}

	scanner := NewCVEStreamScanner(onEvent)
	scanner.windowSize = 10 // small window for testing

	maliciousInput := "some harmless prefix code; rm -rf / some suffix"
	reader := NewScanningReader(strings.NewReader(maliciousInput), scanner)

	buf := make([]byte, 10)
	var totalRead int
	var err error

	for {
		var n int
		n, err = reader.Read(buf)
		totalRead += n
		if err != nil {
			break
		}
	}

	if !errors.Is(err, ErrCVEDetected) {
		t.Errorf("expected ErrCVEDetected, got: %v", err)
	}

	// Verify events were triggered
	hasStarted := false
	hasDetected := false
	for _, ev := range events {
		if ev == EventScanStarted {
			hasStarted = true
		}
		if ev == EventCVEDetected {
			hasDetected = true
		}
	}

	if !hasStarted {
		t.Error("expected EventScanStarted to be triggered")
	}
	if !hasDetected {
		t.Error("expected EventCVEDetected to be triggered")
	}
}

func TestScanningReader_CleanPayloadFlow(t *testing.T) {
	var events []SecurityEvent
	onEvent := func(event SecurityEvent, details map[string]interface{}) {
		events = append(events, event)
	}

	scanner := NewCVEStreamScanner(onEvent)
	cleanInput := "function hello() { return 'world'; }"
	reader := NewScanningReader(strings.NewReader(cleanInput), scanner)

	buf := make([]byte, 5)
	var out bytes.Buffer

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
	}

	if out.String() != cleanInput {
		t.Errorf("expected output %q, got %q", cleanInput, out.String())
	}

	hasStarted := false
	hasCompleted := false
	for _, ev := range events {
		if ev == EventScanStarted {
			hasStarted = true
		}
		if ev == EventScanCompleted {
			hasCompleted = true
		}
		if ev == EventCVEDetected {
			t.Error("unexpected EventCVEDetected triggered on clean payload")
		}
	}

	if !hasStarted {
		t.Error("expected EventScanStarted to be triggered")
	}
	if !hasCompleted {
		t.Error("expected EventScanCompleted to be triggered")
	}
}
