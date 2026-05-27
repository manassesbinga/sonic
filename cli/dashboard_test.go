package cli

import (
	"strings"
	"testing"
)

func TestParseLabels_Empty(t *testing.T) {
	labels := parseLabels("")
	if len(labels) != 0 {
		t.Fatalf("expected empty map, got %v", labels)
	}
}

func TestParseLabels_Single(t *testing.T) {
	labels := parseLabels(`domain="example.com"`)
	if labels["domain"] != "example.com" {
		t.Errorf("expected example.com, got %s", labels["domain"])
	}
}

func TestParseLabels_Multiple(t *testing.T) {
	labels := parseLabels(`domain="example.com",mode="intercept",status="200"`)
	if labels["domain"] != "example.com" {
		t.Errorf("domain: %s", labels["domain"])
	}
	if labels["mode"] != "intercept" {
		t.Errorf("mode: %s", labels["mode"])
	}
	if labels["status"] != "200" {
		t.Errorf("status: %s", labels["status"])
	}
}

func TestParseMetrics_Empty(t *testing.T) {
	metrics := parseMetrics("")
	if len(metrics) != 0 {
		t.Fatalf("expected 0, got %d", len(metrics))
	}
}

func TestParseMetrics_CommentsOnly(t *testing.T) {
	body := `# HELP sonic_requests_total Total requests
# TYPE sonic_requests_total counter`
	metrics := parseMetrics(body)
	if len(metrics) != 0 {
		t.Fatalf("expected 0 from comments only, got %d", len(metrics))
	}
}

func TestParseMetrics_SimpleCounter(t *testing.T) {
	body := `sonic_requests_total{sonic_domain="example.com",sonic_mode="intercept"} 42`
	metrics := parseMetrics(body)
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	m := metrics[0]
	if m.Name != "sonic_requests_total" {
		t.Errorf("name: %s", m.Name)
	}
	if m.Labels["sonic_domain"] != "example.com" {
		t.Errorf("domain: %s", m.Labels["sonic_domain"])
	}
	if m.Labels["sonic_mode"] != "intercept" {
		t.Errorf("mode: %s", m.Labels["sonic_mode"])
	}
	if m.Value != 42 {
		t.Errorf("value: %f", m.Value)
	}
}

func TestParseMetrics_Multiple(t *testing.T) {
	body := `sonic_requests_total{sonic_domain="a.com"} 10
sonic_requests_total{sonic_domain="b.com"} 20
sonic_connections_active 5
sonic_errors_total 0
sonic_vm_pool_size{state="idle"} 32
sonic_vm_pool_size{state="active"} 32`
	metrics := parseMetrics(body)
	if len(metrics) != 6 {
		t.Fatalf("expected 6 metrics, got %d", len(metrics))
	}
}

func TestParseMetrics_NoLabels(t *testing.T) {
	body := `sonic_connections_active 8`
	metrics := parseMetrics(body)
	if len(metrics) != 1 {
		t.Fatalf("expected 1, got %d", len(metrics))
	}
	m := metrics[0]
	if m.Name != "sonic_connections_active" {
		t.Errorf("name: %s", m.Name)
	}
	if len(m.Labels) != 0 {
		t.Errorf("expected no labels, got %v", m.Labels)
	}
	if m.Value != 8 {
		t.Errorf("value: %f", m.Value)
	}
}

func TestParseMetrics_SkipInvalid(t *testing.T) {
	body := `# TYPE sonic_requests_total counter
sonic_requests_total 42
invalid_line_without_value
sonic_connections_active 5`
	metrics := parseMetrics(body)
	if len(metrics) != 2 {
		t.Fatalf("expected 2 valid metrics, got %d", len(metrics))
	}
}

func TestStyleVal_Integer(t *testing.T) {
	if s := styleVal(0, ""); s != "0" {
		t.Errorf("expected 0, got %s", s)
	}
	if s := styleVal(42, ""); s != "42" {
		t.Errorf("expected 42, got %s", s)
	}
}

func TestStyleVal_Thousands(t *testing.T) {
	if s := styleVal(1500, ""); s != "1.5k" {
		t.Errorf("expected 1.5k, got %s", s)
	}
	if s := styleVal(14200, ""); s != "14.2k" {
		t.Errorf("expected 14.2k, got %s", s)
	}
}

func TestStyleVal_Millions(t *testing.T) {
	if s := styleVal(1500000, ""); s != "1.50M" {
		t.Errorf("expected 1.50M, got %s", s)
	}
}

func TestStyleVal_Unit(t *testing.T) {
	if s := styleVal(42, "ms"); s != "42ms" {
		t.Errorf("expected 42ms, got %s", s)
	}
}

func TestStyleVal_Fractional(t *testing.T) {
	v := styleVal(0.5, "")
	if v != "0.500" && v != "0.5" {
		t.Errorf("expected 0.500-ish, got %s", v)
	}
}

func TestSumMetrics_Single(t *testing.T) {
	metrics := []MetricValue{
		{Name: "sonic_requests_total", Value: 42},
	}
	if sum := sumMetrics(metrics, "sonic_requests_total"); sum != 42 {
		t.Errorf("expected 42, got %f", sum)
	}
}

func TestSumMetrics_Multiple(t *testing.T) {
	metrics := []MetricValue{
		{Name: "sonic_requests_total", Labels: map[string]string{"domain": "a.com"}, Value: 10},
		{Name: "sonic_requests_total", Labels: map[string]string{"domain": "b.com"}, Value: 20},
		{Name: "sonic_errors_total", Value: 1},
	}
	if sum := sumMetrics(metrics, "sonic_requests_total"); sum != 30 {
		t.Errorf("expected 30, got %f", sum)
	}
	if sum := sumMetrics(metrics, "sonic_errors_total"); sum != 1 {
		t.Errorf("expected 1, got %f", sum)
	}
}

func TestSumMetrics_NotFound(t *testing.T) {
	metrics := []MetricValue{
		{Name: "sonic_requests_total", Value: 42},
	}
	if sum := sumMetrics(metrics, "sonic_nonexistent"); sum != 0 {
		t.Errorf("expected 0, got %f", sum)
	}
}

func TestFindMetric_Exact(t *testing.T) {
	metrics := []MetricValue{
		{Name: "sonic_requests_total", Labels: map[string]string{"domain": "a.com"}, Value: 10},
		{Name: "sonic_requests_total", Labels: map[string]string{"domain": "b.com"}, Value: 20},
	}
	v := findMetric(metrics, "sonic_requests_total", map[string]string{"domain": "a.com"})
	if v != 10 {
		t.Errorf("expected 10, got %f", v)
	}
}

func TestFindMetric_NotFound(t *testing.T) {
	metrics := []MetricValue{
		{Name: "sonic_requests_total", Labels: map[string]string{"domain": "a.com"}, Value: 10},
	}
	v := findMetric(metrics, "sonic_requests_total", map[string]string{"domain": "b.com"})
	if v != 0 {
		t.Errorf("expected 0, got %f", v)
	}
}

func TestTopDomains(t *testing.T) {
	metrics := []MetricValue{
		{Name: "sonic_requests_total", Labels: map[string]string{"sonic_domain": "a.com"}, Value: 100},
		{Name: "sonic_requests_total", Labels: map[string]string{"sonic_domain": "b.com"}, Value: 50},
		{Name: "sonic_requests_total", Labels: map[string]string{"sonic_domain": "c.com"}, Value: 25},
	}

	domains := topDomains(metrics, 2)
	if len(domains) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(domains))
	}
	if domains[0][0] != "a.com" || domains[1][0] != "b.com" {
		t.Errorf("expected a.com then b.com, got %s then %s", domains[0][0], domains[1][0])
	}
}

func TestTopDomains_Empty(t *testing.T) {
	domains := topDomains(nil, 5)
	if len(domains) != 0 {
		t.Errorf("expected 0, got %d", len(domains))
	}
}

func TestRenderDashboard_ShowsData(t *testing.T) {
	metrics := []MetricValue{
		{Name: "sonic_requests_total", Labels: map[string]string{"sonic_domain": "example.com"}, Value: 100},
		{Name: "sonic_connections_active", Value: 5},
		{Name: "sonic_vm_pool_size", Value: 64},
		{Name: "sonic_errors_total", Value: 0},
	}

	output := RenderDashboard(metrics)
	if !strings.Contains(output, "100") {
		t.Error("dashboard should show request count")
	}
	if !strings.Contains(output, "5") {
		t.Error("dashboard should show active connections")
	}
	if !strings.Contains(output, "64") {
		t.Error("dashboard should show pool size")
	}
	if !strings.Contains(output, "example.com") {
		t.Error("dashboard should show top domains")
	}
}

func TestTruncStr_Short(t *testing.T) {
	if s := truncStr("hello", 10); s != "hello" {
		t.Errorf("expected hello, got %s", s)
	}
}

func TestTruncStr_Long(t *testing.T) {
	s := truncStr("this-is-a-very-long-domain-name.example.com", 22)
	runes := []rune(s)
	if len(runes) != 22 {
		t.Errorf("expected 22 runes, got %d runes: %s", len(runes), s)
	}
	if string(runes[len(runes)-1]) != "…" {
		t.Errorf("expected ellipsis at end, got %s", string(runes[len(runes)-1]))
	}
}
