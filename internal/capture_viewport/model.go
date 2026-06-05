package capture_viewport

import (
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

	current int // bar numerator
	total   int // bar denominator; 0 = indeterminate
	done    bool
	err     error
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
	if m.total > 0 {
		ratio := float64(m.current) / float64(m.total)
		b.WriteString("  ")
		b.WriteString(m.progress.ViewAs(ratio))
	}
	b.WriteByte('\n')

	for _, line := range m.tail {
		b.WriteString(tailStyle.Render("│ " + line))
		b.WriteByte('\n')
	}
	return b.String()
}
