// Command capture-viewport-demo drives the WET capture viewport with
// synthetic plan/progress/log events so the prototype UX can be eyeballed
// on a real TTY. Throwaway spike artifact — see
// docs/plans/2026-06-05-capture-progress-prototype.md (#28). Remove or
// promote once the rendering levers are decided.
package main

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/amarbel-llc/cutting-garden/internal/capture_viewport"
	cgp "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

func main() {
	m := capture_viewport.New(capture_viewport.WithTitle("capture .tmp/cap-fixture"))
	p := tea.NewProgram(m)

	go func() {
		r := capture_viewport.NewReporter(p)
		const total = 30
		r.Plan(cgp.ReportPlan{Items: total, Label: "capture .tmp/cap-fixture"})
		for i := 1; i <= total; i++ {
			time.Sleep(120 * time.Millisecond)
			r.Progress(cgp.ReportProgress{
				Item:  fmt.Sprintf("file-%02d.txt", i),
				Items: int64(i),
			})
			if i%10 == 0 {
				r.Log("store group %d flushed", i/10)
			}
		}
		time.Sleep(250 * time.Millisecond)
		p.Send(capture_viewport.BatchDone{Err: nil})
	}()

	if _, err := p.Run(); err != nil {
		fmt.Println("demo error:", err)
	}
}
