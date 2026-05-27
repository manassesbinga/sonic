package sonic_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manassesbinga/sonic/cli"
)

func startFakeMetricsServer(t testing.TB, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/metrics") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(body))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestCLI_DashboardFetchMetrics(t *testing.T) {
	body := `# HELP sonic_requests_total Total requests
# TYPE sonic_requests_total counter
sonic_requests_total{sonic_domain="example.com",sonic_mode="intercept",http_status_code="200"} 1420
sonic_connections_active 8
sonic_vm_pool_size 64
sonic_errors_total 0
sonic_request_duration_ms_count 1420
sonic_request_duration_ms_sum 2840`
	srv := startFakeMetricsServer(t, body)
	defer srv.Close()

	// Fetch metrics from fake server
	addr := strings.TrimPrefix(srv.URL, "http://")
	metrics, err := cli.FetchMetrics(addr)
	if err != nil {
		t.Fatalf("FetchMetrics failed: %v", err)
	}

	// Validate parsed metrics
	var foundReqTotal, foundConns, foundPool, foundErrs bool
	for _, m := range metrics {
		switch m.Name {
		case "sonic_requests_total":
			if m.Value == 1420 {
				foundReqTotal = true
			}
		case "sonic_connections_active":
			if m.Value == 8 {
				foundConns = true
			}
		case "sonic_vm_pool_size":
			if m.Value == 64 {
				foundPool = true
			}
		case "sonic_errors_total":
			if m.Value == 0 {
				foundErrs = true
			}
		}
	}
	if !foundReqTotal {
		t.Error("sonic_requests_total not parsed correctly")
	}
	if !foundConns {
		t.Error("sonic_connections_active not parsed correctly")
	}
	if !foundPool {
		t.Error("sonic_vm_pool_size not parsed correctly")
	}
	if !foundErrs {
		t.Error("sonic_errors_total not parsed correctly")
	}
}

func TestCLI_DashboardRender(t *testing.T) {
	body := `sonic_requests_total{sonic_domain="example.com"} 100
sonic_requests_total{sonic_domain="api.example.com"} 50
sonic_requests_total{sonic_domain="cdn.example.com"} 25
sonic_connections_active 5
sonic_vm_pool_size 64
sonic_errors_total 0`
	srv := startFakeMetricsServer(t, body)
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	metrics, err := cli.FetchMetrics(addr)
	if err != nil {
		t.Fatalf("FetchMetrics failed: %v", err)
	}

	output := cli.RenderDashboard(metrics)

	if !strings.Contains(output, "175") {
		t.Error("dashboard should show total requests (100+50+25=175)")
	}
	if !strings.Contains(output, "5") {
		t.Error("dashboard should show active connections")
	}
	if !strings.Contains(output, "64") {
		t.Error("dashboard should show VM pool size")
	}
	if !strings.Contains(output, "example.com") {
		t.Error("dashboard should list top domains")
	}
}

func TestCLI_DashboardWithErrors(t *testing.T) {
	body := `sonic_requests_total{sonic_domain="example.com"} 500
sonic_connections_active 3
sonic_vm_pool_size 64
sonic_errors_total 2`
	srv := startFakeMetricsServer(t, body)
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	metrics, err := cli.FetchMetrics(addr)
	if err != nil {
		t.Fatalf("FetchMetrics failed: %v", err)
	}

	output := cli.RenderDashboard(metrics)
	if !strings.Contains(output, "2") {
		t.Error("dashboard should show error count")
	}
}

func TestCLI_DashboardServerDown(t *testing.T) {
	_, err := cli.FetchMetrics("localhost:19999")
	if err == nil {
		t.Fatal("expected error when connecting to non-existent server")
	}
}

func TestCLI_DashboardEmptyMetrics(t *testing.T) {
	body := `# TYPE sonic_requests_total counter
# HELP sonic_requests_total Total requests`
	srv := startFakeMetricsServer(t, body)
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	metrics, err := cli.FetchMetrics(addr)
	if err != nil {
		t.Fatalf("FetchMetrics failed: %v", err)
	}

	output := cli.RenderDashboard(metrics)
	if !strings.Contains(output, "0") {
		t.Error("dashboard should show zeros for empty metrics")
	}
}

func TestCLI_DashboardManyDomains(t *testing.T) {
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, fmt.Sprintf(`sonic_requests_total{sonic_domain="host-%04d.example.com"} %d`, i, 100-i))
	}
	body := strings.Join(lines, "\n") + "\nsonic_connections_active 1\nsonic_vm_pool_size 4\nsonic_errors_total 0\n"
	srv := startFakeMetricsServer(t, body)
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	metrics, err := cli.FetchMetrics(addr)
	if err != nil {
		t.Fatalf("FetchMetrics failed: %v", err)
	}

	output := cli.RenderDashboard(metrics)
	// Should show top 5 domains
	domainCount := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "host-") {
			domainCount++
		}
	}
	if domainCount != 5 {
		t.Errorf("expected 5 domains, got %d", domainCount)
	}
}
