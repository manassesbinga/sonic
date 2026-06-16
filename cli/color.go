package cli

import (
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	ocGreen  = lipgloss.Color("#50fa7b")
	ocText   = lipgloss.Color("#d2d2d2")
	ocDim    = lipgloss.Color("#8b8b8b")
	ocBorder = lipgloss.Color("#383838")
	ocRed    = lipgloss.Color("#ff5555")

	StylePrimary = lipgloss.NewStyle().
			Foreground(ocGreen).
			Bold(true)

	StyleAccent = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#3fb950")).
			Bold(true)

	StyleWarning = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#d29922"))

	StyleError = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f85149")).
			Bold(true)

	StyleMuted = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#8b949e"))

	StyleText = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#e6edf3"))

	StyleCyan = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#39d2c0"))

	StyleBold = lipgloss.NewStyle().Bold(true)

	bannerStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#58a6ff")).
			Padding(1, 3).
			Width(48).
			Align(lipgloss.Center)

	statusBoxStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Width(50)

	sectionStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderBottom(true).
			BorderForeground(lipgloss.Color("#30363d")).
			Padding(0, 2).
			Width(60).
			Align(lipgloss.Center)

	kvStyle = lipgloss.NewStyle().
			PaddingLeft(4)

	kvKeyStyle = StylePrimary.Copy().
			Width(18).
			Align(lipgloss.Right).
			PaddingRight(1)
)

func Primary(s string) string { return StylePrimary.Render(s) }
func Accent(s string) string   { return StyleAccent.Render(s) }
func Warning(s string) string  { return StyleWarning.Render(s) }
func Error(s string) string    { return StyleError.Render(s) }
func Muted(s string) string    { return StyleMuted.Render(s) }
func Text(s string) string     { return StyleText.Render(s) }
func Cyan(s string) string     { return StyleCyan.Render(s) }
func Bold(s string) string     { return StyleBold.Render(s) }

func success(msg string) { writeLog("SUCCESS", msg) }
func warn(msg string)    { writeLog("WARN", msg) }
func info(msg string)    { writeLog("INFO", msg) }

func fail(msg string) {
	writeLog("ERROR", msg)
	b := statusBoxStyle.Copy().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#f85149")).
		Foreground(lipgloss.Color("#f85149")).
		Render(msg)
	fmt.Fprintln(os.Stderr, b)
}

func Section(title string) {
	fmt.Println()
	b := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#30363d")).
		Padding(0, 2).
		Render(StylePrimary.Render(title))
	fmt.Println("  " + b)
}

func KeyValue(key, value string) {
	line := lipgloss.JoinHorizontal(lipgloss.Left,
		kvKeyStyle.Render(key+":"),
		StyleText.Render(value),
	)
	fmt.Println(kvStyle.Render(line))
}

func KeyValueStatus(key, value string, ok bool) {
	status := LipglossColor("#3fb950", "active")
	if !ok {
		status = LipglossColor("#f85149", "inactive")
	}
	line := lipgloss.JoinHorizontal(lipgloss.Left,
		kvKeyStyle.Render(key+":"),
		StyleText.Render(value),
		StyleMuted.Render("  ("+status+")"),
	)
	fmt.Println(kvStyle.Render(line))
}

func LipglossColor(color, text string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(text)
}


func PrintBanner(version string) {
	fmt.Println()
	content := StyleCyan.Bold(true).Render("SONIC v"+version) + "\n" +
		StyleMuted.Render("Multi-Language, Multi-Protocol Edge Engine") + "\n" +
		StyleMuted.Render("eBPF-accelerated  KV Store  WASM/JS/Native")
	fmt.Println(bannerStyle.Render(content))
	fmt.Println()
}

func PrintArrow(text string)  { fmt.Println("  " + text) }
func PrintBullet(text string) { fmt.Println("  " + text) }
func HR()                     { fmt.Println() }

func HRWithLabel(label string) {
	fmt.Println()
	fmt.Println("  " + sectionStyle.Render(StylePrimary.Render(label)))
	fmt.Println()
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}


func RenderTable(headers []string, rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	ncols := len(headers)
	if ncols == 0 && len(rows) > 0 {
		ncols = len(rows[0])
	}
	if ncols == 0 {
		return ""
	}

	colWidths := make([]int, ncols)
	for i, h := range headers {
		colWidths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < ncols && len(cell) > colWidths[i] {
				colWidths[i] = len(cell)
			}
		}
	}
	for i := range colWidths {
		if colWidths[i] < 3 {
			colWidths[i] = 3
		}
		colWidths[i] += 2
	}

	var lines []string
	hCells := make([]string, ncols)
	for i, h := range headers {
		w := colWidths[i]
		hCells[i] = StylePrimary.Bold(true).Render(fmt.Sprintf("%-*s", w, " "+h+" "))
	}
	lines = append(lines, "  "+strings.Join(hCells, ""))

	for _, row := range rows {
		cells := make([]string, ncols)
		for i, cell := range row {
			if i >= ncols {
				break
			}
			w := colWidths[i]
			cells[i] = fmt.Sprintf("%-*s", w, " "+cell+" ")
		}
		lines = append(lines, "  "+strings.Join(cells, ""))
	}
	return strings.Join(lines, "\n")
}

