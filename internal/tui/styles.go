package tui

import "github.com/charmbracelet/lipgloss"

var (
	title = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	dim   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	warn  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	bad   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	good  = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))

	category = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("246"))
	cursorOn = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	help     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return itoa(n) + " B"
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return trim(float64(n)/float64(div)) + " " + []string{"KB", "MB", "GB", "TB"}[exp]
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func trim(f float64) string {
	whole := int64(f)
	frac := int64((f - float64(whole)) * 10)
	return itoa(whole) + "." + itoa(frac)
}
