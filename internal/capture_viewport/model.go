package capture_viewport

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const defaultTailLines = 5

// Model renders a spinner + rolling log tail and, when a total is known, a
// progress bar. Driven entirely by the messages in messages.go.
type Model struct {
	title    string
	tailMax  int
	tail     []string
	spinner  spinner.Model
	progress progress.Model

	current    int   // item-bar numerator
	total      int   // item-bar denominator; 0 = indeterminate
	bytesDone  int64 // bytes processed so far (byte bar / counter)
	bytesTotal int64 // total bytes; 0 = unknown
	done       bool
	err        error
}

// Option configures a Model.
type Option func(*Model)

// WithTailLines sets the rolling-tail height (default 5). TUNING LEVER.
func WithTailLines(n int) Option { return func(m *Model) { m.tailMax = n } }

// WithTitle sets the header label.
func WithTitle(s string) Option { return func(m *Model) { m.title = s } }

// New builds a Model ready for tea.NewProgram.
func New(opts ...Option) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	m := Model{
		tailMax:  defaultTailLines,
		spinner:  sp,
		progress: progress.New(progress.WithDefaultGradient()),
	}
	for _, o := range opts {
		o(&m)
	}
	return m
}

func (m Model) Init() tea.Cmd { return m.spinner.Tick }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case LogLine:
		// Dedupe consecutive identical lines: a streaming source that
		// re-reports the same item label every tick (yt-dlp's video id)
		// would otherwise flood the tail. Distinct labels (git hashes)
		// are unaffected since each differs from its predecessor.
		if n := len(m.tail); n > 0 && m.tail[n-1] == msg.Text {
			return m, nil
		}
		m.tail = append(m.tail, msg.Text)
		if len(m.tail) > m.tailMax {
			m.tail = m.tail[len(m.tail)-m.tailMax:]
		}
		return m, nil
	case OperationStarted:
		if msg.Name != "" {
			m.title = msg.Name
		}
		if msg.Total > 0 {
			m.total = msg.Total
		}
		if msg.Index > 0 {
			m.current = msg.Index - 1
		}
		return m, nil
	case OperationProgress:
		m.current = msg.Current
		if msg.Total > 0 {
			m.total = msg.Total
		}
		m.bytesDone = msg.Bytes
		if msg.BytesTotal > 0 {
			m.bytesTotal = msg.BytesTotal
		}
		return m, nil
	case OperationDone:
		if msg.Err != nil {
			m.err = msg.Err
		} else {
			m.tail = nil // collapse on success
		}
		return m, nil
	case BatchDone:
		m.done = true
		m.err = msg.Err
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	default:
		return m, nil
	}
}

var (
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	failStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	tailStyle    = lipgloss.NewStyle().Faint(true)
)

func (m Model) View() string {
	var b strings.Builder

	switch {
	case m.done && m.err == nil:
		b.WriteString(successStyle.Render("✓ " + m.title))
		b.WriteByte('\n')
		return b.String()
	case m.done && m.err != nil:
		b.WriteString(failStyle.Render("✗ " + m.title + ": " + m.err.Error()))
		b.WriteByte('\n')
		return b.String()
	}

	b.WriteString(m.spinner.View())
	b.WriteByte(' ')
	b.WriteString(m.title)
	switch {
	case m.total > 0:
		// Item-count bar (e.g. git structural objects). Unchanged.
		ratio := float64(m.current) / float64(m.total)
		b.WriteString("  ")
		b.WriteString(m.progress.ViewAs(ratio))
	case m.bytesTotal > 0:
		// Byte bar with humanized counts (e.g. a yt-dlp stream whose
		// total_bytes_estimate is known).
		ratio := float64(m.bytesDone) / float64(m.bytesTotal)
		b.WriteString("  ")
		b.WriteString(m.progress.ViewAs(ratio))
		b.WriteByte(' ')
		b.WriteString(humanizeBytes(m.bytesDone))
		b.WriteByte('/')
		b.WriteString(humanizeBytes(m.bytesTotal))
	case m.bytesDone > 0:
		// Indeterminate byte counter: bytes are flowing but no total is
		// known yet (yt-dlp's total_bytes is NA until the final tick).
		b.WriteByte(' ')
		b.WriteString(humanizeBytes(m.bytesDone))
	}
	b.WriteByte('\n')

	for _, line := range m.tail {
		b.WriteString(tailStyle.Render("│ " + line))
		b.WriteByte('\n')
	}
	return b.String()
}

// humanizeBytes formats n as a binary-prefixed size with one decimal
// place (e.g. 1024 -> "1.0 KiB"). Values below 1 KiB render as whole
// bytes ("512 B") since a fractional byte count is meaningless.
func humanizeBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
