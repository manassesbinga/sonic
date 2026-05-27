package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/olekukonko/tablewriter"
)

var (
	reset  = "\033[0m"
	bold   = "\033[1m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	blue   = "\033[34m"
	cyanC  = "\033[36m"
	magenta = "\033[35m"
)

func color(s, c string) string {
	if c == "" || s == "" {
		return s
	}
	return c + s + reset
}

func Red(s string) string     { return color(s, red) }
func Green(s string) string   { return color(s, green) }
func Yellow(s string) string  { return color(s, yellow) }
func Blue(s string) string    { return color(s, blue) }
func Cyan(s string) string    { return color(s, cyanC) }
func Magenta(s string) string { return color(s, magenta) }
func Bold(s string) string    { return color(s, bold) }

var (
	StyleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00ADD8")).
			PaddingLeft(2).
			PaddingRight(2)

	StyleSuccess = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00FF00")).
			Bold(true)

	StyleError = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF0000")).
			Bold(true)

	StyleWarning = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFF00"))

	StyleInfo = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#87CEEB"))

	StyleKey = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFD700"))

	StyleValue = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF"))

	StyleBox = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("#00ADD8")).
			Padding(1, 2)

	StyleSection = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF69B4")).
			Underline(true)
)

func success(msg string) {
	fmt.Fprintln(os.Stderr, Green("✓")+" "+msg)
}

func SuccessBox(msg string) {
	box := StyleBox.Render(StyleSuccess.Render("✓ ") + msg)
	fmt.Fprintln(os.Stderr, box)
}

func warn(msg string) {
	fmt.Fprintln(os.Stderr, Yellow("⚠")+" "+msg)
}

func info(msg string) {
	fmt.Fprintln(os.Stderr, Cyan("ℹ")+" "+msg)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, Red("✗")+" "+msg)
}

func ErrorBox(msg string) {
	box := StyleBox.Render(StyleError.Render("✗ ") + msg)
	fmt.Fprintln(os.Stderr, box)
}

func Section(title string) {
	fmt.Println()
	fmt.Println("  " + StyleSection.Render(title))
	fmt.Println()
}

func KeyValue(key, value string) {
	fmt.Printf("    %s %s\n", StyleKey.Render(fmt.Sprintf("%-12s", key+":")), StyleValue.Render(value))
}

func KeyValueStatus(key, value string, ok bool) {
	status := Green("✓")
	if !ok {
		status = Red("✗")
	}
	fmt.Printf("    %s %s %s\n", StyleKey.Render(fmt.Sprintf("%-12s", key+":")), StyleValue.Render(value), status)
}

func NewTable() *tablewriter.Table {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAutoFormatHeaders(true)
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetCenterSeparator("")
	table.SetColumnSeparator("")
	table.SetRowSeparator("")
	table.SetHeaderLine(false)
	table.SetBorder(false)
	table.SetTablePadding("  ")
	table.SetNoWhiteSpace(true)
	return table
}

func NewPrettyTable(headers []string) *tablewriter.Table {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader(headers)
	table.SetAutoWrapText(false)
	table.SetAutoFormatHeaders(true)
	table.SetHeaderAlignment(tablewriter.ALIGN_CENTER)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetCenterSeparator("│")
	table.SetColumnSeparator("│")
	table.SetRowSeparator("─")
	table.SetHeaderLine(true)
	table.SetBorder(true)
	table.SetTablePadding("  ")
	table.SetHeaderColor(
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
	)
	return table
}

func PrintKeyValuePairs(title string, pairs [][2]string) {
	if title != "" {
		Section(title)
	}
	maxKeyLen := 0
	for _, p := range pairs {
		if len(p[0]) > maxKeyLen {
			maxKeyLen = len(p[0])
		}
	}
	for _, p := range pairs {
		key := fmt.Sprintf("%-*s", maxKeyLen, p[0])
		KeyValue(key, p[1])
	}
}

func PrintBox(title, content string) {
	styledTitle := StyleHeader.Render(title)
	fullContent := styledTitle + "\n\n" + content
	fmt.Println(StyleBox.Render(fullContent))
}

func PrintBanner(version string) {
	banner := `
  ╔══════════════════════════════════════════════════════╗
  ║                     %s                           ║
  ║  Multi-Language, Multi-Protocol Edge Engine       ║
  ║  eBPF-accelerated | KV Store | WASM/JS/Native ║
  ╚══════════════════════════════════════════════════════╝
`
	fmt.Printf(Cyan(banner), Bold("SONIC v"+version))
}

func PrintCheckmark(text string) {
	fmt.Printf("  %s %s\n", Green("✓"), text)
}

func PrintCross(text string) {
	fmt.Printf("  %s %s\n", Red("✗"), text)
}

func PrintArrow(text string) {
	fmt.Printf("  %s %s\n", Cyan("▶"), text)
}

func PrintBullet(text string) {
	fmt.Printf("  %s %s\n", Yellow("•"), text)
}

func HR() {
	width := 60
	fmt.Println("  " + strings.Repeat("─", width))
}

func HRWithLabel(label string) {
	width := 60
	labelLen := len(label) + 4
	if labelLen >= width {
		fmt.Println("  " + label)
		return
	}
	dashLen := (width - labelLen) / 2
	left := strings.Repeat("─", dashLen)
	right := strings.Repeat("─", width-labelLen-dashLen)
	fmt.Printf("  %s[ %s ]%s\n", left, Bold(label), right)
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
