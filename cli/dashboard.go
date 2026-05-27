package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

type MetricValue struct {
	Name   string
	Labels map[string]string
	Value  float64
}

func FetchMetrics(addr string) ([]MetricValue, error) {
	resp, err := http.Get(fmt.Sprintf("http://%s/metrics", addr))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metrics: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read metrics body: %w", err)
	}

	return parseMetrics(string(body)), nil
}

func parseMetrics(body string) []MetricValue {
	var metrics []MetricValue
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse: metric_name{labels} value
		openBrace := strings.Index(line, "{")
		closeBrace := strings.Index(line, "}")

		var name string
		var labels map[string]string
		var rest string

		if openBrace >= 0 && closeBrace > openBrace {
			name = line[:openBrace]
			labelStr := line[openBrace+1 : closeBrace]
			labels = parseLabels(labelStr)
			rest = strings.TrimSpace(line[closeBrace+1:])
		} else {
			spaceIdx := strings.Index(line, " ")
			if spaceIdx < 0 {
				continue
			}
			name = line[:spaceIdx]
			rest = strings.TrimSpace(line[spaceIdx+1:])
		}

		// Handle space before value after closing brace
		if rest == "" {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				name = fields[0]
				rest = fields[len(fields)-1]
			}
		}

		if rest == "" {
			continue
		}

		val, err := strconv.ParseFloat(rest, 64)
		if err != nil {
			continue
		}

		if labels == nil {
			labels = make(map[string]string)
		}
		metrics = append(metrics, MetricValue{Name: name, Labels: labels, Value: val})
	}
	return metrics
}

func parseLabels(s string) map[string]string {
	labels := make(map[string]string)
	if s == "" {
		return labels
	}

	parts := strings.Split(s, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		eqIdx := strings.Index(part, "=")
		if eqIdx < 0 {
			continue
		}
		key := strings.TrimSpace(part[:eqIdx])
		val := strings.TrimSpace(part[eqIdx+1:])
		val = strings.Trim(val, "\"")
		labels[key] = val
	}
	return labels
}

func styleVal(val float64, unit string) string {
	var display string
	if val >= 1_000_000_000 {
		display = fmt.Sprintf("%.2fB", val/1_000_000_000)
	} else if val >= 1_000_000 {
		display = fmt.Sprintf("%.2fM", val/1_000_000)
	} else if val >= 1_000 {
		display = fmt.Sprintf("%.1fk", val/1_000)
	} else if val == float64(int64(val)) {
		display = fmt.Sprintf("%d", int64(val))
	} else if val < 0.01 {
		display = fmt.Sprintf("%.4f", val)
	} else if val < 1 {
		display = fmt.Sprintf("%.3f", val)
	} else {
		display = fmt.Sprintf("%.1f", val)
	}
	return display + unit
}

func findMetric(metrics []MetricValue, name string, labels map[string]string) float64 {
	for _, m := range metrics {
		if m.Name == name {
			match := true
			for k, v := range labels {
				if m.Labels[k] != v {
					match = false
					break
				}
			}
			if match {
				return m.Value
			}
		}
	}
	return 0
}

func sumMetrics(metrics []MetricValue, name string) float64 {
	var sum float64
	for _, m := range metrics {
		if m.Name == name {
			sum += m.Value
		}
	}
	return sum
}

func topDomains(metrics []MetricValue, limit int) [][3]string {
	type domainCount struct {
		domain string
		count  float64
	}
	counts := make(map[string]float64)
	for _, m := range metrics {
		if m.Name == "sonic_requests_total" {
			domain := m.Labels["sonic_domain"]
			if domain == "" {
				domain = "unknown"
			}
			counts[domain] += m.Value
		}
	}

	var sorted []domainCount
	for d, c := range counts {
		sorted = append(sorted, domainCount{d, c})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	total := sumMetrics(metrics, "sonic_requests_total")

	var result [][3]string
	for i := 0; i < limit && i < len(sorted); i++ {
		pct := 0.0
		if total > 0 {
			pct = (sorted[i].count / total) * 100
		}
		result = append(result, [3]string{sorted[i].domain, styleVal(sorted[i].count, ""), fmt.Sprintf("%.0f%%", pct)})
	}
	return result
}

var (
	dashBorder = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("#00ADD8")).
			Padding(0, 1)
	dashTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00ADD8")).
			Align(lipgloss.Center).
			Width(56)
	dashLabel = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFD700"))
	dashValue = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF"))
	dashGreen = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00FF00"))
	dashRed = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF0000"))
	dashDim = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888"))
	dashBar = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00ADD8"))
)

func bar(width int, pct float64) string {
	if pct <= 0 {
		return ""
	}
	filled := int(float64(width) * pct / 100.0)
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled)
}

func RenderDashboard(metrics []MetricValue) string {
	var b strings.Builder

	// Banner
	b.WriteString("\n")
	b.WriteString(dashTitle.Render("╔══════════════════════════════════════════╗"))
	b.WriteString("\n")
	b.WriteString(dashTitle.Render("║           SONIC DASHBOARD               ║"))
	b.WriteString("\n")
	b.WriteString(dashTitle.Render("╚══════════════════════════════════════════╝"))
	b.WriteString("\n\n")

	reqTotal := sumMetrics(metrics, "sonic_requests_total")

	b.WriteString(fmt.Sprintf("%s %s\n",
		dashLabel.Render("Requests total:"),
		dashValue.Render(styleVal(reqTotal, "")),
	))
	b.WriteString(fmt.Sprintf("%s %s\n",
		dashLabel.Render("Active conns:"),
		dashValue.Render(styleVal(sumMetrics(metrics, "sonic_connections_active"), "")),
	))

	b.WriteString(fmt.Sprintf("%s %s\n",
		dashLabel.Render("VM pool:"),
		dashValue.Render(styleVal(sumMetrics(metrics, "sonic_vm_pool_size"), "")),
	))

	errTotal := sumMetrics(metrics, "sonic_errors_total")
	errStr := dashGreen.Render(styleVal(errTotal, ""))
	if errTotal > 0 {
		errStr = dashRed.Render(styleVal(errTotal, ""))
	}
	b.WriteString(fmt.Sprintf("%s %s\n\n",
		dashLabel.Render("Errors:"),
		errStr,
	))

	// Top domains
	b.WriteString(dashDim.Render("────────── Top Domains ──────────"))
	b.WriteString("\n")
	domains := topDomains(metrics, 5)
	maxPct := 100.0
	if len(domains) > 0 {
		last, _ := strconv.ParseFloat(strings.TrimSuffix(domains[0][2], "%"), 64)
		if last > 0 {
			maxPct = last
		}
	}
	for _, d := range domains {
		pctStr := strings.TrimSuffix(d[2], "%")
		pct, _ := strconv.ParseFloat(pctStr, 64)
		barLen := int((pct / maxPct) * 20)
		if barLen < 1 && pct > 0 {
			barLen = 1
		}
		barStr := ""
		if barLen > 0 {
			barStr = dashBar.Render(strings.Repeat("█", barLen))
		}
		b.WriteString(fmt.Sprintf("  %-22s %s %s (%s)\n",
			dashValue.Render(truncStr(d[0], 22)),
			barStr,
			dashDim.Render(d[1]),
			dashDim.Render(d[2]),
		))
	}

	return dashBorder.Render(b.String())
}

func truncStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current proxy metrics",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		addr := metricsAddr
		if addr == "" {
			addr = "localhost:9090"
		}

		metrics, err := FetchMetrics(addr)
		if err != nil {
			fail(fmt.Sprintf("cannot connect to metrics server at %s: %v", addr, err))
			os.Exit(1)
		}

		fmt.Println(RenderDashboard(metrics))
	},
}

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Live metrics dashboard (refreshes every 2s)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		addr := metricsAddr
		if addr == "" {
			addr = "localhost:9090"
		}

		fmt.Print("\033[2J")
		fmt.Print("\033[?25l")
		defer fmt.Print("\033[?25h")

		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()

		for {
			fmt.Print("\033[H")

			metrics, err := FetchMetrics(addr)
			if err != nil {
				fmt.Print("\033[J")
				fail(fmt.Sprintf("cannot connect to metrics at %s: %v", addr, err))
				fmt.Println()
				fmt.Println(dashDim.Render("Retrying in 2s..."))
				<-tick.C
				continue
			}

			fmt.Print("\033[J")
			fmt.Println(RenderDashboard(metrics))
			fmt.Println(dashDim.Render("  [Ctrl+C] exit  |  " + time.Now().Format("15:04:05")))

			select {
			case <-tick.C:
			}
		}
	},
}

var metricsAddr string

func init() {
	statusCmd.Flags().StringVarP(&metricsAddr, "addr", "a", "localhost:9090", "metrics server address (host:port)")
	dashboardCmd.Flags().StringVarP(&metricsAddr, "addr", "a", "localhost:9090", "metrics server address (host:port)")
}
